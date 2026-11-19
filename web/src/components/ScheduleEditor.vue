<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { api, post, errorText, date, type Config, type Resource } from '../lib/api'
import { identity } from '../lib/identity'
import { usePanel } from '../stores/panel'
import AppDialog from './AppDialog.vue'
import ScheduleCycle from './ScheduleCycle.vue'
const open = defineModel<boolean>({ required: true })
const props = defineProps<{ editing?: Resource; initial?: Config }>()
const emit = defineEmits(['saved'])
const panel = usePanel(),
  busy = ref(false),
  loading = ref(false),
  error = ref(''),
  name = ref(''),
  config = ref<Config>({}),
  capability = ref<Config>({ users: ['opsview-task'], interpreters: [] }),
  next = ref<string[]>([]),
  args = ref('[]'),
  environment = ref('{}'),
  headers = ref('{}')
const types: Config = {
  backup: '网站 / 数据库备份',
  directory: '目录备份',
  panel: '面板备份',
  shell: 'Shell 脚本',
  python: 'Python 脚本',
  http: 'HTTP 请求',
  rotate: '网站日志切割',
  cleanup: '日志清理',
  renew: '证书续期',
  'site-state': '网站启停',
  'time-sync': '时间同步',
}
const s3Storage = computed(() => panel.resources.filter(r => r.kind === 'backup' && r.config.destination === 's3'))
const script = computed(() => ['shell', 'python'].includes(config.value.operation))
const targets = computed(() =>
  panel.resources.filter(
    (r) => r.kind === (config.value.operation === 'backup' ? 'backup' : 'site'),
  ),
)
let openSequence = 0,
  loadedRevision = 0
let previewSequence = 0,
  timer: ReturnType<typeof setTimeout> | undefined
onBeforeUnmount(() => {
  openSequence++
  previewSequence++
  clearTimeout(timer)
  config.value = {}
  environment.value = '{}'
  headers.value = '{}'
})
watch(open, async (value) => {
  const seq = ++openSequence
  previewSequence++
  clearTimeout(timer)
  if (!value) {
    config.value = {}
    environment.value = '{}'
    headers.value = '{}'
    error.value = ''
    return
  }
  busy.value = false
  error.value = ''
  loading.value = true
  next.value = []
  try {
    const defaults = await api('/settings')
    const editing = props.editing
      ? await api<Resource>('/schedules/' + props.editing.id)
      : undefined
    if (seq !== openSequence || !open.value) return
    loadedRevision = editing?.revision || 0
    config.value = {
      operation: 'backup',
      cron: '0 3 * * *',
      enabled: false,
      category: '默认分类',
      timezone: defaults.timezone || 'Local',
      retentionDays: 30,
      user: 'opsview-task',
      workingDirectory: '/tmp',
      timeoutSeconds: 3600,
      interpreter: '/usr/bin/python3',
      script: '',
      method: 'GET',
      expectedStatus: 200,
      url: 'https://',
      olderDays: 30,
      preview: true,
      retention: 7,
      storageDirectory: defaults.backupDirectory || '/www/backup',
      storageId: '',
      compress: true,
      keepLogs: 30,
      minBytes: 0,
      state: 'stop',
      ...props.initial,
      ...editing?.config,
    }
    name.value = editing?.name || ''
    if (config.value.operation === 'backup' && !config.value.targetIds)
      config.value.targetIds = config.value.targetId ? [config.value.targetId] : []
    args.value = JSON.stringify(config.value.args || [], null, 2)
    environment.value = JSON.stringify(config.value.environment || {}, null, 2)
    headers.value = JSON.stringify(config.value.headers || {}, null, 2)
    try {
      const result = await api('/schedules/capabilities')
      if (seq !== openSequence) return
      capability.value = result
    } catch {
      /* manual capabilities remain disabled when no executor */
    }
    void preview()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    if (seq === openSequence) loading.value = false
  }
})
watch(
  () => [config.value.cron, config.value.timezone],
  () => {
    clearTimeout(timer)
    timer = setTimeout(preview, 300)
  },
)
watch(
  () => config.value.operation,
  (op, old) => {
    if (op === 'http' && old !== 'http' && config.value.timeoutSeconds > 300)
      config.value.timeoutSeconds = 30
  },
)
async function preview() {
  if (!open.value) return
  const seq = ++previewSequence
  try {
    const result = await post('/schedules/preview', {
      cron: config.value.cron,
      timezone: config.value.timezone,
    })
    if (seq === previewSequence) next.value = result.next
  } catch {
    if (seq === previewSequence) next.value = []
  }
}
async function save() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const c = { ...config.value }
    if (script.value) {
      c.args = JSON.parse(args.value)
      c.environment = JSON.parse(environment.value)
    }
    if (c.operation === 'http') c.headers = JSON.parse(headers.value)
    const auth = c.user === 'root' && script.value ? await identity() : undefined
    await api(props.editing ? '/resources/' + props.editing.id : '/resources', {
      method: props.editing ? 'PUT' : 'POST',
      headers: auth ? { 'X-OpsView-Password': auth.password, 'X-OpsView-TOTP': auth.code } : {},
      body: JSON.stringify({
        name: name.value,
        config: c,
        ...(props.editing ? { revision: loadedRevision } : { kind: 'schedule' }),
      }),
    })
    open.value = false
    emit('saved')
    ElMessage.success('计划已保存')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <AppDialog
    v-model="open"
    :title="editing ? '编辑计划任务' : '创建计划任务'"
    width="850px"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div v-loading="loading">
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <el-form label-width="112px" @submit.prevent="save">
        <el-form-item label="任务类型">
          <el-select v-model="config.operation">
            <el-option v-for="(label, key) in types" :key="key" :value="key" :label="label" />
          </el-select>
        </el-form-item>
        <el-form-item label="任务名称" required>
          <el-input v-model="name" maxlength="80" aria-label="任务名称" />
        </el-form-item>
        <el-form-item label="分类">
          <el-input v-model="config.category" maxlength="40" />
        </el-form-item>
        <el-form-item label="执行周期"><ScheduleCycle v-model="config.cron" /></el-form-item>
        <el-form-item label="时区">
          <el-input v-model="config.timezone" placeholder="Asia/Shanghai" />
        </el-form-item>
        <el-form-item label="未来五次执行">
          <span>
            {{ next.length ? next.map((v) => date(v)).join('；') : '请填写有效的五段 Cron 和时区' }}
          </span>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="config.enabled" aria-label="启用" />
        </el-form-item>
        <el-form-item label="日志保留">
          <el-input-number v-model="config.retentionDays" :min="1" :max="365" />
          天
        </el-form-item>
        <el-form-item v-if="config.operation === 'backup'" label="备份目标" required>
          <el-select v-model="config.targetIds" multiple filterable>
            <el-option v-if="config.operation === 'renew'" value="panel" label="面板证书" />
            <el-option v-for="r in targets" :key="r.id" :value="r.id" :label="r.name" />
          </el-select>
          <router-link to="/backups?tab=records">管理备份配置</router-link>
        </el-form-item>
        <el-form-item
          v-if="['rotate', 'renew', 'site-state'].includes(config.operation)"
          label="目标"
          required
        >
          <el-select v-model="config.targetId" filterable>
            <el-option v-if="config.operation === 'renew'" value="panel" label="面板证书" />
            <el-option v-for="r in targets" :key="r.id" :value="r.id" :label="r.name" />
          </el-select>
          <router-link v-if="config.operation === 'backup'" to="/backups?tab=records">
            管理备份配置
          </router-link>
        </el-form-item>
        <template v-if="script">
          <el-alert
            v-if="config.user === 'root'"
            title="root 脚本拥有完整系统权限。保存、启用及执行需要验证管理员身份。"
            type="warning"
            :closable="false"
          />
          <el-form-item label="执行用户">
            <el-select v-model="config.user">
              <el-option v-for="u in capability.users" :key="u" :value="u" :label="u" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="config.operation === 'python'" label="解释器">
            <el-select v-model="config.interpreter">
              <el-option v-for="p in capability.interpreters" :key="p" :value="p" :label="p" />
            </el-select>
          </el-form-item>
          <el-form-item label="工作目录">
            <el-input v-model="config.workingDirectory" />
          </el-form-item>
          <el-form-item label="超时秒数">
            <el-input-number v-model="config.timeoutSeconds" :min="60" :max="86400" />
          </el-form-item>
          <el-form-item label="脚本内容">
            <el-input v-model="config.script" type="textarea" :rows="10" spellcheck="false" />
          </el-form-item>
          <el-form-item label="参数 JSON 数组">
            <el-input v-model="args" type="textarea" :rows="2" />
          </el-form-item>
          <el-form-item label="私密变量 JSON">
            <el-input v-model="environment" type="textarea" :rows="3" autocomplete="off" />
            <small>值加密保存；已遮盖的值保持原样。</small>
          </el-form-item>
        </template>
        <template v-if="config.operation === 'http'">
          <el-form-item label="方法">
            <el-select v-model="config.method">
              <el-option value="GET" />
              <el-option value="POST" />
            </el-select>
          </el-form-item>
          <el-form-item label="URL">
            <el-input v-model="config.url" placeholder="https://example.com/path?key=value" />
          </el-form-item>
          <el-form-item label="请求头 JSON">
            <el-input v-model="headers" type="textarea" :rows="3" />
          </el-form-item>
          <el-form-item v-if="config.method === 'POST'" label="请求体">
            <el-input v-model="config.body" type="textarea" :rows="4" />
          </el-form-item>
          <el-form-item label="超时秒数">
            <el-input-number v-model="config.timeoutSeconds" :min="1" :max="300" />
          </el-form-item>
          <el-form-item label="预期状态码">
            <el-input-number v-model="config.expectedStatus" :min="100" :max="599" />
          </el-form-item>
          <el-form-item label="访问内网">
            <el-checkbox v-model="config.allowPrivate">
              明确允许本机和内网目标；云元数据地址始终禁止
            </el-checkbox>
          </el-form-item>
        </template>
        <template v-if="['directory', 'cleanup'].includes(config.operation)">
          <el-form-item label="目录">
            <el-input v-model="config.path" placeholder="绝对路径" />
          </el-form-item>
        </template>
        <template v-if="config.operation === 'directory'">
          <el-form-item label="保留份数">
            <el-input-number v-model="config.retention" :min="1" :max="100" />
          </el-form-item>
          <el-form-item label="存储目录">
            <el-input v-model="config.storageDirectory" :placeholder="'/www/backup'" />
          </el-form-item>
          <el-form-item label="远端存储">
            <el-select v-model="config.storageId">
              <el-option label="仅本地" value="" />
              <el-option v-for="r in s3Storage" :key="r.id" :value="r.id" :label="r.name" />
            </el-select>
          </el-form-item>
          <el-form-item label="排除项">
            <el-input v-model="config.excludes" placeholder="空格分隔，例如 *.tmp cache" />
          </el-form-item>
        </template>
        <template v-if="config.operation === 'cleanup'">
          <el-form-item label="保留天数">
            <el-input-number v-model="config.olderDays" :min="1" :max="3650" />
          </el-form-item>
          <el-form-item label="仅预览"><el-switch v-model="config.preview" /></el-form-item>
          <el-alert
            title="仅处理所选日志目录中的普通 .log/.log.gz 文件，不递归跟随链接。请先预览再启用实际清理。"
            type="warning"
            :closable="false"
          />
        </template>
        <template v-if="config.operation === 'rotate'">
          <el-form-item label="大小门槛 B">
            <el-input-number v-model="config.minBytes" :min="0" />
            <small>0 按计划轮转，非零只在达到大小时轮转</small>
          </el-form-item>
          <el-form-item label="压缩归档"><el-switch v-model="config.compress" /></el-form-item>
          <el-form-item label="保留份数">
            <el-input-number v-model="config.keepLogs" :min="1" :max="3650" />
          </el-form-item>
        </template>
        <el-form-item v-if="config.operation === 'site-state'" label="操作">
          <el-select v-model="config.state">
            <el-option label="停止网站" value="stop" />
            <el-option label="启动网站" value="start" />
          </el-select>
        </el-form-item>
        <p class="form-help">同一任务不重叠执行；停机期间错过的任务不补跑。脚本失败不自动重试。</p>
      </el-form>
    </div>
    <template #footer>
      <el-button :disabled="busy" @click="open = false">取消</el-button>
      <el-button type="primary" :loading="busy" :disabled="loading" @click="save">保存</el-button>
    </template>
  </AppDialog>
</template>
