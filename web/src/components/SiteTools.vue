<script setup lang="ts">
import DialogActions from './DialogActions.vue'
import AppDialog from './AppDialog.vue'
import { computed, ref, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, action, post, errorText, size, date, type Config, type Resource } from '../lib/api'
import { usePanel } from '../stores/panel'
import SoftwareSettings from './SoftwareSettings.vue'
const props = defineProps<{ nginx: Resource[] }>()
const emit = defineEmits<{ settings: [site: Resource, page: string] }>()
const panel = usePanel(),
  dialog = ref(''),
  busy = ref(false),
  error = ref(''),
  results = ref<Config[]>([])
const instanceId = ref(''),
  selectedId = ref(''),
  settingsOpen = ref(false),
  security = ref<Config>({})
const sites = computed(() => panel.kind('site'))
const instance = computed(
  () =>
    props.nginx.find((r) => r.id === instanceId.value) ||
    props.nginx.find((r) => r.status === 'ready') ||
    props.nginx[0],
)
const selected = computed(() => sites.value.find((r) => r.id === selectedId.value))
let reads: AbortController | undefined
let generation = 0
function cancel() {
  generation++
  reads?.abort()
  busy.value = false
}
onBeforeUnmount(cancel)
function selectSite() {
  const c = selected.value?.config || {}
  security.value = {
    securityHeaders: !!c.securityHeaders,
    denyFrames: !!c.denyFrames,
    denyUploadPHP: !!c.denyUploadPHP,
  }
}
async function show(kind: string) {
  cancel()
  dialog.value = kind
  error.value = ''
  results.value = []
  if (kind === 'security') {
    selectedId.value = sites.value[0]?.id || ''
    selectSite()
    return
  }
  await load(kind)
}
async function load(kind = dialog.value) {
  cancel()
  const sequence = generation
  reads = new AbortController()
  const signal = reads.signal
  busy.value = true
  results.value = []
  const current = [...sites.value]
  for (let i = 0; i < current.length && !signal.aborted; i += 4) {
    const batch = await Promise.all(
      current.slice(i, i + 4).map(async (site) => {
        try {
          return {
            site,
            ...(await api(`/sites/${site.id}/${kind === 'traffic' ? 'traffic' : 'security'}`, {
              signal,
            })),
          }
        } catch (e) {
          return { site, error: errorText(e) }
        }
      }),
    )
    if (sequence === generation) results.value.push(...batch)
  }
  if (sequence === generation) busy.value = false
}
async function nginxAction(op: string) {
  const r = instance.value
  if (!r) return
  if (op === 'settings') {
    settingsOpen.value = true
    return
  }
  try {
    const labels: Config = { start: '启动', stop: '停止', reload: '平滑重载', restart: '重启' }
    const affected = sites.value.filter((s) => s.config.instanceId === r.id)
    await ElMessageBox.confirm(
      `${labels[op]} ${r.name} 将影响 ${affected.length} 个关联网站${op === 'restart' || op === 'stop' ? '，可能中断访问' : ''}。确认继续？`,
      'Nginx 服务',
      { confirmButtonText: '确认', cancelButtonText: '取消' },
    )
    await action(r.id, op)
    ElMessage.success('服务任务已提交')
    await panel.refresh()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function applySecurity() {
  const r = selected.value
  if (!r) return
  try {
    await ElMessageBox.confirm(
      '将检查并重载该站点配置。同源嵌入限制可能影响第三方 iframe；上传目录 PHP 限制适用于 /upload、/uploads、/storage。',
      '应用网站安全',
      { confirmButtonText: '检查并应用', cancelButtonText: '取消' },
    )
    await post('/sites/' + r.id + '/settings', {
      name: r.name,
      config: { ...r.config, ...security.value },
      revision: r.revision,
    })
    ElMessage.success('安全配置任务已提交')
    await panel.refresh()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
const totalBytes = computed(() => results.value.reduce((n, r) => n + Number(r.todayBytes || 0), 0))
</script>
<template>
  <el-button @click="nginxAction('settings')">高级设置</el-button>
  <el-button @click="show('traffic')">网站统计</el-button>
  <el-button @click="show('scan')">漏洞扫描</el-button>
  <el-button @click="show('security')">网站安全</el-button>
  <el-select
    v-if="nginx.length > 1"
    v-model="instanceId"
    :placeholder="instance?.name"
    aria-label="网站入口实例"
    style="width: 170px"
  >
    <el-option v-for="r in nginx" :key="r.id" :value="r.id" :label="r.name" />
  </el-select>
  <el-dropdown trigger="click" @command="nginxAction">
    <el-button>Nginx {{ instance?.config.version }} ▾</el-button>
    <template #dropdown>
      <el-dropdown-menu>
        <el-dropdown-item command="settings">服务与配置</el-dropdown-item>
        <el-dropdown-item command="reload">平滑重载 Nginx</el-dropdown-item>
        <el-dropdown-item command="restart">重启 Nginx</el-dropdown-item>
        <el-dropdown-item command="start">启动 Nginx</el-dropdown-item>
        <el-dropdown-item command="stop">停止 Nginx</el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
  <SoftwareSettings v-model="settingsOpen" :resource="instance" />
  <AppDialog
    :model-value="!!dialog"
    @update:model-value="
      (v: boolean) => {
        if (!v) {
          dialog = ''
          cancel()
        }
      }
    "
    :title="
      dialog === 'traffic'
        ? '网站统计总览'
        : dialog === 'scan'
          ? '漏洞扫描 · 本机基础检查'
          : '网站安全'
    "
    width="min(980px,96vw)"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <template v-if="dialog === 'traffic'">
      <div class="tool-summary">
        <span>
          今日响应流量：{{ size(totalBytes) }} · 已读取
          {{ results.filter((r) => !r.error).length }} / {{ sites.length }} 个站点
        </span>
        <el-button :loading="busy" @click="load()">更新统计</el-button>
      </div>
      <el-table :data="results" max-height="480">
        <el-table-column label="网站" min-width="150">
          <template #default="{ row }">{{ row.site.name }}</template>
        </el-table-column>
        <el-table-column label="今日流量">
          <template #default="{ row }">{{ row.error ? '不可用' : size(row.todayBytes) }}</template>
        </el-table-column>
        <el-table-column label="服务器时区" prop="timezone" />
        <el-table-column label="结果" min-width="180">
          <template #default="{ row }">{{ row.error || '已采集' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button
              link
              type="primary"
              @click="
                () => {
                  emit('settings', row.site, 'traffic')
                  dialog = ''
                  cancel()
                }
              "
            >
              详情
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </template>
    <template v-else-if="dialog === 'scan'">
      <el-alert
        title="仅检查本机配置、证书、常见敏感文件和目录权限；不包含 CVE 数据库、代码审计或远程漏洞探测。未发现问题不代表不存在漏洞。"
        type="info"
        :closable="false"
      />
      <div class="tool-summary">
        <span>已检查 {{ results.length }} / {{ sites.length }} 个站点</span>
        <el-button v-if="busy" @click="cancel">取消检查</el-button>
        <el-button v-else @click="load()">重新检查</el-button>
      </div>
      <el-collapse>
        <el-collapse-item
          v-for="r in results"
          :key="r.site.id"
          :name="r.site.id"
          :title="
            r.site.name +
            ' · ' +
            (r.error
              ? '检查失败'
              : r.checks.filter((c: Config) => c.status === 'warning').length + ' 项需核对')
          "
        >
          <el-alert v-if="r.error" :title="r.error" type="error" :closable="false" />
          <template v-else>
            <small>检查时间：{{ date(r.checkedAt) }}</small>
            <el-table :data="r.checks">
              <el-table-column prop="name" label="检查项" width="140" />
              <el-table-column label="结果" width="100">
                <template #default="{ row }">
                  <el-tag
                    :type="
                      row.status === 'pass'
                        ? 'success'
                        : row.status === 'warning'
                          ? 'warning'
                          : 'info'
                    "
                  >
                    {{
                      (
                        { pass: '通过', warning: '需核对', unknown: '未知', info: '说明' } as Config
                      )[row.status]
                    }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="说明" prop="detail" min-width="250" />
            </el-table>
          </template>
        </el-collapse-item>
      </el-collapse>
    </template>
    <template v-else-if="dialog === 'security'">
      <el-empty v-if="!sites.length" description="请先添加站点" />
      <el-form v-else label-width="150px">
        <el-form-item label="网站">
          <el-select v-model="selectedId" @change="selectSite">
            <el-option v-for="r in sites" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="安全响应头">
          <el-switch v-model="security.securityHeaders" />
          <span class="hint">nosniff 与 Referrer-Policy</span>
        </el-form-item>
        <el-form-item label="仅允许同源嵌入">
          <el-switch v-model="security.denyFrames" />
        </el-form-item>
        <el-form-item v-if="selected?.config.type === 'php'" label="禁止上传目录 PHP">
          <el-switch v-model="security.denyUploadPHP" />
        </el-form-item>
        <el-form-item label="敏感文件保护">
          <span>静态站与 PHP 站默认禁止访问隐藏文件及 SQL、备份、配置、日志文件。</span>
        </el-form-item>
        <DialogActions>
          <el-button type="primary" @click="applySecurity">保存并应用</el-button>
          <el-button
            @click="
              () => {
                emit('settings', selected!, 'ssl')
                dialog = ''
              }
            "
          >
            SSL 证书
          </el-button>
        </DialogActions>
      </el-form>
    </template>
  </AppDialog>
</template>
<style scoped>
.tool-summary {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin: 12px 0;
}
.hint {
  margin-left: 10px;
  color: var(--el-text-color-secondary);
}
</style>
