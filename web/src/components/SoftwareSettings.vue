<script setup lang="ts">
import DataList from './DataList.vue'
import { computed, ref, watch, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, action, post, errorText, type Config, type Resource } from '../lib/api'
import { usePanel } from '../stores/panel'
import CodeEditor from './CodeEditor.vue'
const props = defineProps<{ modelValue: boolean; resource?: Resource }>()
const emit = defineEmits(['update:modelValue'])
const panel = usePanel(),
  page = ref('service'),
  loading = ref(false),
  error = ref(''),
  info = ref<Config>({}),
  document = ref<Config>({}),
  text = ref(''),
  values = ref<Record<string, string>>({}),
  logText = ref(''),
  metrics = ref<Config>({}),
  extensions = ref<any[]>([]),
  selectedFile = ref('main'),
  search = ref(''),
  task = ref(''),
  port = ref(0)
const diffVisible = ref(false),
  pendingApply = ref<{ mode: string; source: 'text' | 'values' }>({
    mode: 'restart',
    source: 'values',
  })
const targetVersion = ref(''),
  scopeSite = ref(''),
  session = ref<Config>({ handler: 'files' }),
  sessionRedis = ref(''),
  sessionMemcached = ref({ port: 11211, saslEnabled: false, saslUsername: '', saslPassword: '' })
const memoryBudget = ref<Config>({}),
  memoryTier = ref(1024)
const logResult = ref<Config>({}),
  logRange = ref<[Date, Date] | null>(null),
  logLevel = ref(''),
  logKind = ref('error'),
  logArchive = ref(''),
  autoLogs = ref(false),
  logBottom = ref(true),
  logElement = ref<HTMLElement>()
let logTimer: ReturnType<typeof setInterval> | undefined
const fileLog = computed(
  () => ['error', 'slow'].includes(page.value) && !(sw.value === 'redis' && page.value === 'slow'),
)
const levels = [
  ['debug', '调试'],
  ['info', '信息'],
  ['notice', '通知'],
  ['warning', '警告'],
  ['error', '错误'],
  ['critical', '严重'],
  ['alert', '告警'],
  ['emergency', '紧急'],
]
let logRequest = 0
async function readLogs() {
  const request = ++logRequest
  const query = new URLSearchParams({
    kind: page.value === 'error' ? logKind.value : page.value,
    siteId: scopeSite.value,
    search: search.value,
    level: logLevel.value,
    archive: logArchive.value,
  })
  if (logRange.value) {
    query.set('since', logRange.value[0].toISOString())
    query.set('until', logRange.value[1].toISOString())
  }
  const result = await api(url('logs') + '?' + query)
  if (request !== logRequest || !visible.value) return
  logResult.value = result
  logText.value = result.content || result.notice || '暂无匹配日志'
  if (logBottom.value && logElement.value) {
    requestAnimationFrame(() => logElement.value?.scrollTo({ top: logElement.value.scrollHeight }))
  }
}
function resetLogFilters() {
  search.value = ''
  logRange.value = null
  logLevel.value = ''
  void readLogs().catch((e) => (error.value = errorText(e)))
}
async function manageLog(remove = false) {
  try {
    await ElMessageBox.confirm(
      remove
        ? '永久删除选定归档，无法恢复。当前日志和其他归档会保留。'
        : '将当前日志完整归档，并建立新日志文件。Nginx 在线重开日志；其他软件须先停止对应服务。归档占用磁盘，需要时可单独删除。',
      remove ? '删除日志归档' : '归档并清理当前日志',
      { type: 'warning', confirmButtonText: '确认操作', cancelButtonText: '取消' },
    )
    await submit(remove ? 'delete-log-archive' : 'rotate-log', {
      kind: page.value === 'error' ? logKind.value : page.value,
      siteId: scopeSite.value,
      archive: logArchive.value,
      baseRevision: logResult.value.revision,
      confirmed: true,
    })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
function mib(value: number) {
  return ((value || 0) / 1048576).toFixed(1) + ' MiB'
}
async function estimateMemory(preset = false) {
  try {
    const query = preset
      ? '?budgetMB=' + memoryTier.value
      : '?values=' + encodeURIComponent(JSON.stringify(values.value))
    memoryBudget.value = await api(url('memory-budget') + query)
    if (preset) values.value = { ...memoryBudget.value.values }
  } catch (e) {
    error.value = errorText(e)
  }
}
const guardian = ref({
  enabled: true,
  autostart: true,
  restartSeconds: 3,
  attempts: 5,
  memoryMB: 0,
  oomScore: 0,
})
const visible = computed({ get: () => props.modelValue, set: (v) => emit('update:modelValue', v) })
const sw = computed(() => props.resource?.config.software || '')
const menus = computed(
  () =>
    (
      ({
        nginx: [
          ['service', '服务'],
          ['config', '配置文件'],
          ['version', '版本切换'],
          ['metrics', '负载状态'],
          ['performance', '性能调整'],
          ['error', '错误日志'],
        ],
        mysql: [
          ['service', '服务'],
          ['config', '配置文件'],
          ['storage', '存储位置'],
          ['port', '端口配置'],
          ['metrics', '当前状态'],
          ['performance', '性能调整'],
          ['error', '错误日志'],
          ['slow', '慢日志'],
          ['binlog', '二进制日志'],
          ['memory', '内存保护'],
          ['version', '版本迁移'],
        ],
        php: [
          ['service', '服务'],
          ['extensions', '安装扩展'],
          ['general', '配置修改'],
          ['upload', '上传限制'],
          ['timeout', '超时限制'],
          ['config', '配置文件'],
          ['fpm', 'FPM 配置文件'],
          ['functions', '禁用函数'],
          ['performance', '性能调整'],
          ['metrics', '负载状态'],
          ['session', 'Session 配置'],
          ['error', '日志'],
          ['slow', '慢日志'],
          ['phpinfo', 'phpinfo'],
        ],
        redis: [
          ['service', '服务'],
          ['config', '配置文件'],
          ['port', '端口与访问'],
          ['persistence', '持久化'],
          ['performance', '性能调整'],
          ['metrics', '负载状态'],
          ['slow', '慢日志'],
          ['error', '运行日志'],
          ['backup', '数据备份'],
          ['version', '版本迁移'],
        ],
      }) as Record<string, string[][]>
    )[sw.value] || [['service', '服务']],
)
const fields = computed(() =>
  (document.value.fields || []).filter(
    (f: any) => f.group === page.value || (page.value === 'binlog' && f.group === 'binlog'),
  ),
)
const dynamicEligible = computed(() => {
  const changed = (document.value.fields || []).filter(
    (f: any) => values.value[f.key] !== document.value.values?.[f.key],
  )
  return changed.length > 0 && changed.every((f: any) => f.apply === 'dynamic')
})
const editable = computed(() => !!info.value.editable)
const file = computed(() => (page.value === 'fpm' ? 'fpm' : selectedFile.value))
watch([autoLogs, visible, fileLog, logArchive], () => {
  if (logTimer) clearInterval(logTimer)
  logTimer = undefined
  if (autoLogs.value && visible.value && fileLog.value && !logArchive.value) {
    logTimer = setInterval(() => {
      void readLogs().catch((e) => (error.value = errorText(e)))
    }, 5000)
  }
})
const changeDiff = computed(() => {
  if (pendingApply.value.source === 'values')
    return (
      Object.entries(values.value)
        .filter(([k, v]) => document.value.values?.[k] !== v)
        .map(([k, v]) => `${k}: ${document.value.values?.[k] ?? '(未设置)'} → ${v}`)
        .join('\n') || '参数没有变化'
    )
  const before = String(document.value.content || '').split('\n'),
    after = text.value.split('\n')
  return (
    before
      .filter((line) => !after.includes(line))
      .map((l) => '- ' + l)
      .concat(after.filter((l) => !before.includes(l)).map((l) => '+ ' + l))
      .join('\n') || '配置没有变化'
  )
})

let timer: ReturnType<typeof setInterval> | undefined
function url(view: string) {
  return `/instances/${props.resource?.id}/${view}`
}
async function readDocument() {
  document.value = await api(
    url('configuration') + '?file=' + file.value + '&siteId=' + scopeSite.value,
  )
  text.value = document.value.content
  values.value = { ...document.value.values }
}
async function load() {
  if (!props.resource) return
  loading.value = true
  error.value = ''
  try {
    info.value = await api(url('settings'))
    port.value = info.value.port
    guardian.value = { ...guardian.value, ...info.value.guardian }
    if (
      [
        'config',
        'fpm',
        'performance',
        'general',
        'upload',
        'timeout',
        'functions',
        'persistence',
        'slow',
        'binlog',
        'port',
        'storage',
      ].includes(page.value) &&
      editable.value
    )
      await readDocument()
    if (page.value === 'session') {
      session.value = await api(url('session') + '?siteId=' + scopeSite.value)
      sessionRedis.value = session.value.instanceId || ''
      sessionMemcached.value = {
        port: 11211,
        saslEnabled: false,
        saslUsername: '',
        saslPassword: '',
        ...session.value.memcached,
      }
    }
    if (page.value === 'metrics') metrics.value = await api(url('metrics'))
    if (page.value === 'performance' && sw.value === 'mysql') await estimateMemory()
    if (['error', 'slow'].includes(page.value)) await readLogs()
    if (['extensions', 'phpinfo'].includes(page.value)) {
      const result = await api(url('extensions'))
      extensions.value = result.extensions
      logText.value = result.runtime
    }
    if (page.value === 'binlog') {
      try {
        const result = await api(url('binlogs'))
        logText.value = result.content
      } catch (e) {
        logText.value = errorText(e)
      }
    }
  } catch (e) {
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      page.value = 'service'
      info.value = {}
      task.value = ''
      scopeSite.value = ''
      logArchive.value = ''
      logResult.value = {}
      void load()
    } else if (timer) {
      clearInterval(timer)
      timer = undefined
    }
  },
)
watch(page, () => {
  ++logRequest
  logResult.value = {}
  logArchive.value = ''
  logKind.value = 'error'
  selectedFile.value =
    sw.value === 'php' && ['performance', 'slow'].includes(page.value) ? 'fpm' : 'main'
  void load()
})
watch(scopeSite, () => {
  ++logRequest
  logArchive.value = ''
  logResult.value = {}
  void load()
})
watch(selectedFile, () => {
  void readDocument().catch((e) => (error.value = errorText(e)))
})
watch(logKind, () => {
  logArchive.value = ''
  if (page.value === 'error') void readLogs().catch((e) => (error.value = errorText(e)))
})
onUnmounted(() => {
  ++logRequest
  if (logTimer) clearInterval(logTimer)
  if (timer) clearInterval(timer)
})
async function submit(operation: string, params: Config = {}) {
  if (!props.resource) return
  try {
    const result = await action(props.resource.id, operation, params)
    task.value = result.id
    ElMessage.success('任务已提交，进度可在任务中心查看')
    if (timer) clearInterval(timer)
    timer = setInterval(async () => {
      await panel.refresh()
      const current = panel.tasks.find((t) => t.id === task.value)
      if (current && ['succeeded', 'failed', 'interrupted', 'cancelled'].includes(current.state)) {
        clearInterval(timer)
        timer = undefined
        if (current.state !== 'succeeded') error.value = current.error || '任务未完成'
        else {
          ElMessage.success('操作完成')
          if (
            operation === 'delete-log-archive' &&
            params.archive === logArchive.value &&
            params.siteId === scopeSite.value
          )
            logArchive.value = ''
          await load()
        }
      }
    }, 2000)
  } catch (e) {
    error.value = errorText(e)
  }
}
async function control(op: string) {
  try {
    await ElMessageBox.confirm(
      `将${{ start: '启动', stop: '停止', restart: '重启', reload: '重载配置' }[op as 'start']} ${props.resource?.name}，关联资源 ${info.value.associated?.length || 0} 个。`,
      '服务操作',
      { confirmButtonText: '确认', cancelButtonText: '取消' },
    )
    await submit(op, sw.value === 'php' ? { siteId: scopeSite.value } : {})
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
function params(source: 'text' | 'values') {
  return {
    file: file.value,
    siteId: scopeSite.value,
    baseRevision: document.value.revision,
    ...(source === 'text' ? { content: text.value } : { values: values.value }),
  }
}
async function switchVersion() {
  try {
    await ElMessageBox.confirm(
      '将校验全部站点，在维护窗口切换 Nginx 入口。失败恢复原版本，业务文件保留。',
      '切换 Nginx 版本',
      { confirmButtonText: '确认切换', cancelButtonText: '取消' },
    )
    await submit('switch-version', {
      targetInstanceId: targetVersion.value,
      maintenanceConfirmed: true,
    })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
async function moveStorage() {
  try {
    await ElMessageBox.confirm(
      '必须先停止关联应用写入。将备份所有数据库、停止实例、复制并验证，旧目录保留。',
      '确认维护窗口',
      { confirmButtonText: '已停止写入，开始迁移', cancelButtonText: '取消' },
    )
    await submit('move-storage', {
      maintenanceConfirmed: true,
      baseRevision: document.value.revision,
    })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
async function saveSession() {
  try {
    await ElMessageBox.confirm('切换会话存储可能使现有用户退出登录，确认应用？', 'Session 配置', {
      confirmButtonText: '确认保存',
      cancelButtonText: '取消',
    })
    await submit('session', {
      operation: 'save',
      siteId: scopeSite.value,
      handler: session.value.handler,
      instanceId: sessionRedis.value,
      ...(session.value.handler === 'memcached'
        ? {
            memcachedPort: sessionMemcached.value.port,
            saslEnabled: sessionMemcached.value.saslEnabled,
            saslUsername: sessionMemcached.value.saslUsername,
            saslPassword: sessionMemcached.value.saslPassword,
          }
        : {}),
      baseRevision: session.value.revision,
      confirmed: true,
    })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
async function cleanSession() {
  try {
    await ElMessageBox.confirm('将删除超过 24 小时未更新的受管会话文件，确认清理？', '清理会话')
    await submit('session', { operation: 'clean', siteId: scopeSite.value, confirmed: true })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
async function validate() {
  try {
    await post(url('validate'), params('text'))
    ElMessage.success('候选配置校验通过')
  } catch (e) {
    error.value = errorText(e)
  }
}
async function save(mode: string, source: 'text' | 'values') {
  if (mode === 'draft') {
    await submit('configure', { ...params(source), apply: 'draft' })
    return
  }
  pendingApply.value = { mode, source }
  diffVisible.value = true
}
async function confirmApply() {
  const { mode, source } = pendingApply.value
  diffVisible.value = false
  await submit('configure', {
    ...params(source),
    apply: mode,
    confirmPersistenceChange: sw.value === 'redis',
  })
}
async function history(revision: string) {
  try {
    const result = await api(
      url('configuration') +
        '?file=' +
        file.value +
        '&siteId=' +
        scopeSite.value +
        '&revision=' +
        revision,
    )
    text.value = result.content
    ElMessage.info('历史版本已载入编辑器，应用前仍需校验')
  } catch (e) {
    error.value = errorText(e)
  }
}
function download() {
  const blob = new Blob([logText.value], { type: 'text/plain;charset=utf-8' }),
    a = documentGlobal.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${sw.value}-${page.value}.log`
  a.click()
  URL.revokeObjectURL(a.href)
}
const documentGlobal = window.document
</script>
<template>
  <el-dialog
    v-model="visible"
    :title="`${sw.toUpperCase()} ${resource?.config.version || ''} · ${resource?.name || ''}`"
    width="min(1120px, 95vw)"
    top="6vh"
    :close-on-click-modal="false"
    class="software-settings"
    destroy-on-close
  >
    <div class="software-settings-layout">
      <aside>
        <button
          v-for="[key, label] in menus"
          :key="key"
          :class="{ active: page === key }"
          @click="page = key"
        >
          {{ label }}
        </button>
      </aside>
      <section v-loading="loading" class="software-settings-content">
        <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
        <div
          v-if="sw === 'php' && !['metrics', 'extensions', 'phpinfo'].includes(page)"
          class="settings-toolbar"
        >
          <span>配置范围</span>
          <el-select v-model="scopeSite">
            <el-option label="版本默认配置（新网站继承）" value="" />
            <el-option
              v-for="r in (info.associated || []).filter((r: any) => r.kind === 'site')"
              :key="r.id"
              :value="r.id"
              :label="r.name"
            />
          </el-select>
        </div>
        <div class="software-context">
          <span>
            {{ resource?.name }} · {{ resource?.config.mode === 'docker' ? 'Docker' : '原生安装' }}
          </span>
          <span>关联资源 {{ info.associated?.length || 0 }} 个</span>
          <el-button text @click="load">刷新</el-button>
        </div>
        <div v-if="page === 'service'">
          <h3>
            当前状态：
            <el-tag :type="info.state === 'active' ? 'success' : 'info'">
              {{ info.state === 'active' ? '运行中' : info.state || '不可用' }}
            </el-tag>
          </h3>
          <div class="service-buttons">
            <el-button :disabled="!editable" @click="control('start')">启动</el-button>
            <el-button :disabled="!editable" @click="control('stop')">停止</el-button>
            <el-button :disabled="!editable" @click="control('restart')">重启</el-button>
            <el-button v-if="info.reload" :disabled="!editable" @click="control('reload')">
              重载配置
            </el-button>
          </div>
          <el-divider />
          <h4>进程守护</h4>
          <p class="form-help">
            异常退出按限制重试；主动停止不会触发守护拉起。修改资源限制后重启生效。
          </p>
          <el-form label-width="150px">
            <el-form-item label="异常重启"><el-switch v-model="guardian.enabled" /></el-form-item>
            <el-form-item label="开机自启"><el-switch v-model="guardian.autostart" /></el-form-item>
            <el-form-item label="重试间隔（秒）">
              <el-input-number v-model="guardian.restartSeconds" :min="1" :max="300" />
            </el-form-item>
            <el-form-item label="五分钟内重试次数">
              <el-input-number v-model="guardian.attempts" :min="1" :max="20" />
            </el-form-item>
          </el-form>
          <el-button type="primary" :disabled="!editable" @click="submit('guardian', guardian)">
            保存守护设置
          </el-button>
          <h4>关联资源</h4>
          <p v-for="r in info.associated" :key="r.id">{{ r.name }} · {{ r.kind }}</p>
          <p v-if="!info.associated?.length" class="form-help">暂无关联资源</p>
          <pre>{{ info.detail }}</pre>
        </div>
        <template v-else-if="page === 'config' || page === 'fpm'">
          <div class="settings-toolbar">
            <el-select v-if="page === 'config'" v-model="selectedFile">
              <el-option
                v-for="f in info.files"
                :key="f"
                :value="f"
                :label="f === 'fpm' ? 'php-fpm.conf' : '主配置文件'"
              />
            </el-select>
            <el-select placeholder="载入历史版本" @change="history">
              <el-option
                v-for="r in document.history"
                :key="r.revision"
                :value="r.revision"
                :label="r.createdAt"
              />
            </el-select>
            <el-button v-if="document.draft" @click="text = document.draft">载入草稿</el-button>
          </div>
          <p class="form-help">
            仅允许修改已验证的参数。用户、路径、模块、凭据和监听设置由独立入口管理。修订号
            {{ document.revision?.slice(0, 12) }}
          </p>
          <CodeEditor v-model="text" />
          <div class="settings-toolbar">
            <el-button :disabled="!editable" @click="validate">校验配置</el-button>
            <el-button :disabled="!editable" @click="save('draft', 'text')">保存草稿</el-button>
            <el-button
              v-if="info.reload"
              type="primary"
              :disabled="!editable"
              @click="save('reload', 'text')"
            >
              保存并重载
            </el-button>
            <el-button :disabled="!editable" @click="save('restart', 'text')">保存并重启</el-button>
          </div>
        </template>
        <template v-else-if="page === 'metrics'">
          <el-alert
            v-if="metrics.unavailable"
            :title="metrics.unavailable"
            type="warning"
            :closable="false"
          />
          <pre v-if="metrics.content">{{ metrics.content }}</pre>
          <DataList
            v-if="metrics.metrics"
            :rows="Object.entries(metrics.metrics).map(([name, value]) => ({ name, value }))"
            max-height="480"
          >
            <el-table-column prop="name" label="指标" />
            <el-table-column prop="value" label="当前值" />
          </DataList>
          <pre v-if="metrics.sites?.length">{{ JSON.stringify(metrics.sites, null, 2) }}</pre>
          <pre>{{ metrics.detail }}</pre>
        </template>
        <template v-else-if="page === 'extensions'">
          <p class="form-help">扩展绑定当前 PHP 版本。内置扩展以实际运行时结果为准。</p>
          <DataList :rows="extensions">
            <el-table-column prop="name" label="扩展" width="140" />
            <el-table-column prop="status" label="状态" width="110" />
            <el-table-column prop="reason" label="说明" />
            <el-table-column label="操作" width="190">
              <template #default="{ row }">
                <el-button
                  v-if="row.editable && !row.loaded"
                  link
                  type="primary"
                  @click="
                    () => {
                      submit('extension', {
                        name: row.name,
                        operation: row.installed ? 'enable' : 'install',
                      })
                    }
                  "
                >
                  安装 / 启用
                </el-button>
                <el-button
                  v-if="row.editable && row.loaded"
                  link
                  @click="submit('extension', { name: row.name, operation: 'disable' })"
                >
                  停用
                </el-button>
                <el-button
                  v-if="row.editable"
                  link
                  type="danger"
                  @click="submit('extension', { name: row.name, operation: 'uninstall' })"
                >
                  卸载
                </el-button>
              </template>
            </el-table-column>
          </DataList>
        </template>
        <template v-else-if="page === 'phpinfo'">
          <h3>PHP 运行时信息</h3>
          <p class="form-help">诊断结果不向网站公开，不展示环境变量和凭据。</p>
          <pre>{{ logText }}</pre>
        </template>
        <template v-else-if="page === 'memory'">
          <h3>内存保护</h3>
          <p class="form-help">
            0 表示沿用系统上限；有限 OOM 优先级不保证免杀。内存过小可能导致数据库重启。
          </p>
          <el-form label-width="150px">
            <el-form-item label="内存上限 MB">
              <el-input-number v-model="guardian.memoryMB" :min="0" :step="128" />
            </el-form-item>
            <el-form-item label="OOM 优先级">
              <el-input-number v-model="guardian.oomScore" :min="-500" :max="500" />
            </el-form-item>
          </el-form>
          <el-button type="primary" @click="submit('guardian', guardian)">保存资源限制</el-button>
        </template>
        <template v-else-if="page === 'port'">
          <h3>端口配置</h3>
          <p>当前端口 {{ info.port }}，默认仅本机访问。云安全组需要单独配置。</p>
          <el-input-number v-model="port" :min="1" :max="65535" />
          <el-button
            type="primary"
            @click="submit('port', { port, baseRevision: document.revision })"
          >
            修改并重启
          </el-button>
        </template>
        <template v-else-if="page === 'storage'">
          <h3>存储位置</h3>
          <p>{{ resource?.config.dataDirectory || info.baseDirectory + '/data' }}</p>
          <p class="form-help">
            迁移前必须停止关联应用写入并完成备份。请在备份与计划中创建备份；当前目录不会通过普通配置编辑器改写。
          </p>
          <el-button
            @click="
              () => {
                $router.push('/backups')
                visible = false
              }
            "
          >
            管理备份
          </el-button>
          <el-button type="primary" @click="moveStorage">迁移至新目录</el-button>
        </template>
        <template v-else-if="page === 'version'">
          <h3>版本迁移</h3>
          <template v-if="sw === 'nginx'">
            <el-select v-model="targetVersion" placeholder="选择已安装的停止中版本">
              <el-option
                v-for="r in panel
                  .kind('instance')
                  .filter(
                    (r) =>
                      r.config.software === 'nginx' &&
                      r.id !== resource?.id &&
                      r.status === 'stopped',
                  )"
                :key="r.id"
                :value="r.id"
                :label="r.name + ' · ' + r.config.version"
              />
            </el-select>
            <el-button type="primary" :disabled="!targetVersion" @click="switchVersion">
              校验并切换入口
            </el-button>
          </template>
          <p class="form-help">
            先在软件市场安装目标版本。数据库通过数据库页面迁移至新实例并验证，源实例和数据保留。
          </p>
          <el-button
            @click="
              () => {
                $router.push(sw === 'mysql' ? '/databases' : '/software')
                visible = false
              }
            "
          >
            {{ sw === 'mysql' ? '选择数据库迁移' : '管理已安装版本' }}
          </el-button>
        </template>
        <template v-else-if="page === 'backup'">
          <h3>数据备份</h3>
          <p class="form-help">
            在备份与计划中选择当前 Redis 实例，可创建经过 RDB
            校验的加密备份，配置保留份数及定时任务；恢复进入另一个空实例。
          </p>
          <el-button
            type="primary"
            @click="
              () => {
                $router.push('/backups')
                visible = false
              }
            "
          >
            管理加密备份与恢复
          </el-button>
          <el-button type="primary" @click="submit('snapshot')">生成 Redis 后台快照</el-button>
          <p class="form-help">
            请求后台保存后，请在负载状态中核对 rdb_last_bgsave_status。快照文件保留在实例 data
            目录。
          </p>
        </template>
        <template v-else-if="page === 'session'">
          <h3>Session 配置</h3>
          <p class="form-help">
            切换可能使已有会话失效。保存前验证扩展与实际会话读写，失败恢复原配置。版本默认配置由新网站继承。
          </p>
          <el-form label-width="150px">
            <el-form-item label="存储方式">
              <el-select v-model="session.handler" aria-label="Session 存储方式">
                <el-option label="文件" value="files" />
                <el-option label="Redis" value="redis" />
                <el-option label="memcached" value="memcached" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="session.handler === 'redis'" label="Redis 实例">
              <el-select v-model="sessionRedis">
                <el-option
                  v-for="r in panel
                    .kind('instance')
                    .filter((r) => r.config.software === 'redis' && r.config.mode === 'native')"
                  :key="r.id"
                  :value="r.id"
                  :label="r.name"
                />
              </el-select>
            </el-form-item>
            <template v-if="session.handler === 'memcached'">
              <el-form-item label="本机端口">
                <el-input-number
                  v-model="sessionMemcached.port"
                  :min="1"
                  :max="65535"
                  aria-label="memcached 端口"
                />
              </el-form-item>
              <el-form-item label="SASL 认证">
                <el-switch
                  v-model="sessionMemcached.saslEnabled"
                  aria-label="memcached SASL 认证"
                />
              </el-form-item>
              <template v-if="sessionMemcached.saslEnabled">
                <el-form-item label="SASL 用户名">
                  <el-input
                    v-model="sessionMemcached.saslUsername"
                    autocomplete="off"
                    aria-label="memcached SASL 用户名"
                  />
                </el-form-item>
                <el-form-item label="SASL 密码">
                  <el-input
                    v-model="sessionMemcached.saslPassword"
                    type="password"
                    show-password
                    autocomplete="new-password"
                    aria-label="memcached SASL 密码"
                  />
                </el-form-item>
              </template>
              <p class="form-help">
                连接已安装的本机 127.0.0.1 服务，需要 PHP memcached 扩展；使用二进制协议。 SASL
                需服务端和扩展均支持。密码占位符保留已有凭据，关闭认证会清除凭据。
              </p>
            </template>
          </el-form>
          <p class="form-help">
            当前目录或前缀：{{ session.namespace || '存储默认值' }}。
            {{
              session.isolated
                ? '已使用该范围独立目录或前缀。'
                : '显式保存后使用该范围独立目录或前缀。'
            }}
          </p>
          <el-button type="primary" :disabled="loading || !editable" @click="saveSession">
            验证连接并保存
          </el-button>
          <p>超过 24 小时未更新的文件：{{ session.expiredFiles || 0 }} 个</p>
          <el-button @click="cleanSession">清理过期会话</el-button>
        </template>
        <template v-else>
          <div v-if="sw === 'mysql' && page === 'performance'" class="notice">
            <div class="settings-toolbar">
              <el-select v-model="memoryTier" style="width: 170px">
                <el-option
                  v-for="n in [512, 1024, 2048, 4096, 8192]"
                  :key="n"
                  :value="n"
                  :label="n + ' MB 预算'"
                />
              </el-select>
              <el-button @click="estimateMemory(true)">填入推荐参数</el-button>
              <el-button @click="estimateMemory()">估算当前参数</el-button>
            </div>
            <template v-if="memoryBudget.estimate">
              <p>
                全局缓存 {{ mib(memoryBudget.estimate.globalBytes) }} · 单连接基础
                {{ mib(memoryBudget.estimate.perConnectionBytes) }} · 基础估算
                {{ mib(memoryBudget.estimate.estimateBytes) }}
              </p>
              <p>
                主机内存 {{ mib(memoryBudget.hostMemoryBytes) }} · 当前可用
                {{ mib(memoryBudget.availableBytes) }}
              </p>
              <small>{{ memoryBudget.estimate.notice }} 档位先填入表单，确认差异后才应用。</small>
            </template>
          </div>
          <el-radio-group
            v-if="sw === 'php' && ['performance', 'timeout'].includes(page)"
            v-model="selectedFile"
          >
            <el-radio-button value="fpm">FPM 参数</el-radio-button>
            <el-radio-button value="main">
              {{ page === 'performance' ? 'OPcache 缓存' : 'PHP 超时' }}
            </el-radio-button>
          </el-radio-group>
          <el-form v-if="fields.length" label-width="210px" class="software-fields">
            <el-form-item v-for="f in fields" :key="f.key" :label="f.label">
              <el-select v-if="f.options?.length" v-model="values[f.key]">
                <el-option v-for="o in f.options" :key="o" :value="o" :label="o" />
              </el-select>
              <el-input v-else v-model="values[f.key]" />
              <small>
                {{ f.key }} ·
                {{
                  f.apply === 'dynamic'
                    ? '动态生效'
                    : f.apply === 'reload'
                      ? '重载生效'
                      : '重启生效'
                }}
              </small>
            </el-form-item>
          </el-form>
          <div v-if="fields.length" class="settings-toolbar">
            <el-button
              type="primary"
              :disabled="!editable"
              @click="
                () => {
                  save(sw === 'redis' ? 'dynamic' : info.reload ? 'reload' : 'restart', 'values')
                }
              "
            >
              保存并{{ sw === 'redis' ? '动态应用' : info.reload ? '重载' : '重启' }}
            </el-button>
            <el-button
              v-if="sw === 'mysql'"
              :disabled="!editable || !dynamicEligible"
              @click="save('dynamic', 'values')"
            >
              保存并动态应用
            </el-button>
            <el-button :disabled="!editable" @click="save('draft', 'values')">保存草稿</el-button>
          </div>
          <template v-if="['error', 'slow', 'binlog'].includes(page)">
            <div class="settings-toolbar">
              <el-select
                v-if="page === 'error'"
                v-model="logKind"
                aria-label="日志文件"
                style="width: 160px"
              >
                <el-option label="运行/错误日志" value="error" />
                <el-option v-if="sw === 'php'" label="FPM 日志" value="fpm" />
                <el-option label="systemd 服务日志" value="service" />
              </el-select>
              <el-input
                v-if="page !== 'binlog' && fileLog"
                v-model="search"
                placeholder="搜索日志"
                @keyup.enter="load"
              />
              <el-button :loading="loading" @click="load">读取</el-button>
              <el-button :disabled="loading" @click="download">下载当前结果</el-button>
            </div>
            <template v-if="fileLog">
              <div class="settings-toolbar">
                <el-date-picker
                  v-model="logRange"
                  type="datetimerange"
                  start-placeholder="开始时间"
                  end-placeholder="结束时间"
                  aria-label="日志时间范围"
                />
                <el-select
                  v-model="logLevel"
                  placeholder="全部级别"
                  clearable
                  aria-label="日志级别"
                  style="width: 130px"
                >
                  <el-option
                    v-for="[key, label] in levels"
                    :key="key"
                    :label="label"
                    :value="key"
                  />
                </el-select>
                <el-button @click="resetLogFilters">重置筛选</el-button>
              </div>
              <div class="settings-toolbar">
                <el-select
                  v-if="logKind !== 'service'"
                  v-model="logArchive"
                  aria-label="日志归档"
                  placeholder="当前日志"
                  style="width: 320px"
                  @change="readLogs().catch((e) => (error = errorText(e)))"
                >
                  <el-option label="当前日志" value="" />
                  <el-option
                    v-for="a in logResult.archives || []"
                    :key="a.name"
                    :value="a.name"
                    :label="`${a.name} · ${mib(a.size)}`"
                  />
                </el-select>
                <el-checkbox v-model="autoLogs" :disabled="!!logArchive">每 5 秒刷新</el-checkbox>
                <el-checkbox v-model="logBottom">滚动到底部</el-checkbox>
                <el-button
                  v-if="logResult.canRotate"
                  :disabled="loading || !editable || !logResult.size"
                  @click="manageLog()"
                >
                  归档并清理
                </el-button>
                <el-button
                  v-if="logArchive"
                  type="danger"
                  :disabled="loading || !editable"
                  @click="manageLog(true)"
                >
                  删除归档
                </el-button>
              </div>
              <p class="form-help">
                时间选择使用浏览器时区；无时区日志按服务器时区
                {{ logResult.timezone || '未知' }}
                解析。级别为精确匹配，无法识别时间的记录在时间筛选时排除。
              </p>
              <p v-if="logResult.windowTruncated || logResult.limited" class="form-help">
                仅扫描文件末尾 1 MiB，最多显示 500 条匹配记录；下载范围与当前结果一致。
              </p>
              <p v-if="logResult.rotationNotice" class="form-help">
                {{ logResult.rotationNotice }}
              </p>
            </template>
            <pre ref="logElement" class="service-log">{{ logText }}</pre>
            <p v-if="page === 'binlog'" class="form-help">
              自动清理需要完整的复制与备份依赖记录，未确认前禁止删除日志文件。
            </p>
          </template>
          <p v-if="['upload', 'timeout'].includes(page)" class="form-help">
            此处修改 PHP 版本默认值；站点覆盖值和 Nginx 限制仍需在对应网站设置中核对。
          </p>
        </template>
        <div v-if="task" class="notice">
          当前任务 {{ task }} ·
          <router-link to="/tasks" @click="visible = false">查看任务进度</router-link>
        </div>
      </section>
    </div>
  </el-dialog>
  <el-dialog v-model="diffVisible" title="确认配置变更" width="min(800px,90vw)" append-to-body>
    <p>
      实例 {{ resource?.name }} · 影响关联资源 {{ info.associated?.length || 0 }} 个 ·
      {{
        pendingApply.mode === 'dynamic'
          ? '动态应用'
          : pendingApply.mode === 'reload'
            ? '平滑重载'
            : '重启服务'
      }}
    </p>
    <p v-if="sw === 'redis'" class="form-help">
      持久化策略变更会影响数据保障和磁盘空间。启用 AOF 将等待后台重写完成。
    </p>
    <pre>{{ changeDiff }}</pre>
    <template #footer>
      <el-button @click="diffVisible = false">取消</el-button>
      <el-button type="primary" @click="confirmApply">确认应用</el-button>
    </template>
  </el-dialog>
</template>
<style scoped>
.software-settings-layout {
  display: grid;
  grid-template-columns: 170px minmax(0, 1fr);
  grid-template-rows: minmax(0, 1fr);
  height: min(720px, calc(100dvh - 180px));
  min-height: 0;
}
.software-settings-layout aside {
  background: #f5f7fa;
  padding: 12px 0;
  border-right: 1px solid #e8edf2;
  min-height: 0;
  overflow: auto;
}
.software-settings-layout aside button {
  display: block;
  width: 100%;
  text-align: left;
  padding: 13px 23px;
  border: 0;
  background: transparent;
  color: #475569;
  cursor: pointer;
  font-size: 14px;
}
.software-settings-layout aside button.active {
  background: white;
  color: #16a34a;
  border-left: 3px solid #16a34a;
}
.software-settings-content {
  padding: 0 26px 24px;
  min-height: 0;
  overflow: auto;
}
.software-context {
  display: flex;
  gap: 16px;
  align-items: center;
  justify-content: space-between;
  color: #94a3b8;
  font-size: 12px;
  margin-bottom: 20px;
}
.service-buttons,
.settings-toolbar {
  display: flex;
  gap: 10px;
  align-items: center;
  margin: 20px 0;
  flex-wrap: wrap;
}
.settings-toolbar .el-select {
  width: 210px;
}
.settings-toolbar .el-input {
  width: 250px;
}
.software-fields .el-input,
.software-fields .el-select {
  max-width: 300px;
}
.software-fields small {
  display: block;
  width: 100%;
  color: #94a3b8;
  font-size: 11px;
}
.service-log,
pre {
  white-space: pre-wrap;
  word-break: break-word;
  background: #f8fafc;
  padding: 16px;
  max-height: 400px;
  overflow: auto;
  font-size: 12px;
}
@media (max-width: 700px) {
  .software-settings-layout {
    grid-template-columns: 120px minmax(0, 1fr);
  }
  .software-settings-content {
    padding: 0 12px;
  }
  .software-settings-layout aside button {
    padding: 12px;
  }
}
</style>
