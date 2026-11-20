<script setup lang="ts">
import LoadingContent from '../components/LoadingContent.vue'
import DialogActions from '../components/DialogActions.vue'
import AppDialog from '../components/AppDialog.vue'
import { queries, isAbort } from '../lib/query'
import { usePageState } from '../composables/pageState'
import DataList from '../components/DataList.vue'
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
import { action, api, post, errorText, size, date, type Resource, type Config } from '../lib/api'
import SiteCreate from '../components/SiteCreate.vue'
import SiteTools from '../components/SiteTools.vue'
import CodeEditor from '../components/CodeEditor.vue'
import SiteRuntimeOptions from '../components/SiteRuntimeOptions.vue'
const route = useRoute()
const reads = new AbortController()
onBeforeUnmount(() => reads.abort())
const panel = usePanel(),
  tab = usePageState('tab', String(route.query.tab || 'php')),
  filter = usePageState('filter', ''),
  createOpen = ref(false),
  categories = ref<string[]>(['默认分类']),
  category = usePageState('category', ''),
  categoryOpen = ref(false),
  categoryName = ref(''),
  categoryOld = ref(''),
  draftName = ref(''),
  baseline = ref(''),
  saving = ref(false),
  selected = ref<Resource>(),
  settingsOpen = ref(false),
  page = ref('general'),
  draft = ref<Config>({}),
  error = ref(''),
  traffic = ref<Record<string, Config>>({}),
  logs = ref(''),
  trafficPeriod = ref('24h'),
  cert = ref({ certificate: '', privateKey: '' })
if (route.query.tab) tab.value = String(route.query.tab)
const nginx = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) =>
        r.config.software === 'nginx' &&
        ['ready', 'running', 'stopped'].includes(r.status) &&
        !r.config.installationIncomplete,
    ),
)
const rows = computed(() =>
  panel
    .kind('site')
    .filter(
      (r) =>
        (tab.value === 'other'
          ? ['app', 'managed'].includes(r.config.type)
          : tab.value === 'php'
            ? ['php', 'static'].includes(r.config.type)
            : r.config.type === tab.value) &&
        (!category.value || (r.config.category || '默认分类') === category.value) &&
        (r.name + ' ' + r.config.domains + ' ' + r.config.note)
          .toLowerCase()
          .includes(filter.value.toLowerCase()),
    ),
)
const initial = computed(() => ({
  type: tab.value === 'other' ? 'managed' : tab.value,
  instanceId: nginx.value.find((r) => r.status === 'ready')?.id || nginx.value[0]?.id,
}))
const relatedBackups = computed(() =>
  panel.kind('backup').filter((r) => r.config.targetId === selected.value?.id),
)
const chart = computed(() => {
  const stats = traffic.value[selected.value?.id || ''] || {}
  const hours = stats.hours || {}
  const hourly = trafficPeriod.value === '24h'
  const count = hourly ? 24 : trafficPeriod.value === '7d' ? 7 : 30
  // Treat server-local labels as calendar labels, independent of the browser timezone.
  const end = new Date(
    (hourly
      ? (stats.serverHour || new Date().toISOString().slice(0, 13)) + ':00:00'
      : (stats.serverDate || new Date().toISOString().slice(0, 10)) + 'T00:00:00') + 'Z',
  )
  return Array.from({ length: count }, (_, i) => {
    const at = new Date(end.getTime() - (count - 1 - i) * (hourly ? 3600000 : 86400000))
    const label = at.toISOString().slice(0, hourly ? 13 : 10)
    const bytes = hourly
      ? Number(hours[label] || 0)
      : Object.entries(hours).reduce(
          (sum, [hour, n]) => sum + (hour.startsWith(label) ? Number(n) : 0),
          0,
        )
    return { hour: label, bytes }
  })
})
watch(
  () => rows.value.map((r) => r.id).join(','),
  async () => {
    const current = [...rows.value]
    for (let i = 0; i < current.length && !reads.signal.aborted; i += 4)
      await Promise.all(current.slice(i, i + 4).map(loadTraffic))
  },
  { immediate: true },
)
function openCreate() {
  createOpen.value = true
}
async function loadCategories() {
  try {
    categories.value = (await api('/site-categories')).rows
  } catch (e) {
    error.value = errorText(e)
  }
}
void loadCategories()
async function changeCategory(remove = false) {
  try {
    await post('/site-categories', { old: categoryOld.value, name: categoryName.value, remove })
    await loadCategories()
    await panel.refresh(['resources'])
    categoryOld.value = ''
    categoryName.value = ''
  } catch (e) {
    error.value = errorText(e)
  }
}
const localSite = computed(() => ['php', 'static'].includes(draft.value.type))
const runtime = computed({
  get: () => (draft.value.type === 'static' ? 'static' : draft.value.phpInstanceId),
  set: (v: string) => {
    draft.value.type = v === 'static' ? 'static' : 'php'
    draft.value.phpInstanceId = v === 'static' ? undefined : v
    draft.value.rewriteTemplate = v === 'static' ? 'none' : undefined
  },
})
const dirty = computed(() => JSON.stringify([draftName.value, draft.value]) !== baseline.value)
async function confirmLeave() {
  if (!dirty.value) return true
  try {
    await ElMessageBox.confirm('当前修改尚未保存，是否放弃？', '未保存的修改', {
      confirmButtonText: '放弃修改',
      cancelButtonText: '继续编辑',
    })
    return true
  } catch {
    return false
  }
}
async function closeSettings(done: () => void) {
  if (saving.value) return
  if (await confirmLeave()) done()
}
async function switchPage(key: string) {
  if (key === page.value) return
  if (dirty.value && !(await confirmLeave())) return
  if (dirty.value && selected.value) {
    draft.value = JSON.parse(JSON.stringify(selected.value.config))
    draftName.value = selected.value.name
    baseline.value = JSON.stringify([draftName.value, draft.value])
  }
  page.value = key
  if (['logs', 'process'].includes(key)) await loadLogs()
}
function phpLabel(row: Resource) {
  if (row.config.type === 'static') return '静态'
  return (
    panel.kind('instance').find((r) => r.id === row.config.phpInstanceId)?.config.version ||
    '关联异常'
  )
}
async function open(row: Resource) {
  selected.value = row
  draft.value = JSON.parse(JSON.stringify(row.config))
  draftName.value = row.name
  baseline.value = JSON.stringify([draftName.value, draft.value])
  settingsOpen.value = true
  page.value = 'general'
  error.value = ''
  await loadTraffic(row)
}
async function loadTraffic(row: Resource) {
  try {
    traffic.value[row.id] = await queries.read(
      `/sites/${row.id}/traffic`,
      (signal) => api(`/sites/${row.id}/traffic`, { signal }),
      15000,
      reads.signal,
    )
  } catch (e) {
    if (!isAbort(e)) error.value = errorText(e)
  }
}
function backupSummary(row: Resource) {
  const items = panel.kind('backup').filter((b) => b.config.targetId === row.id)
  const done = items
    .filter((b) => b.config.artifact || b.config.lastBackupAt)
    .sort((a, b) =>
      String(b.config.lastBackupAt || b.updatedAt).localeCompare(
        String(a.config.lastBackupAt || a.updatedAt),
      ),
    )
  if (done.length) return '已备份 · ' + date(done[0].config.lastBackupAt || done[0].updatedAt)
  return items.length ? '尚未成功备份' : '无备份'
}
function status(row: Resource) {
  if (
    row.config.expirySuspended ||
    (row.config.expiresAt && Date.parse(row.config.expiresAt) <= Date.now())
  )
    return '已到期'
  if (row.config.disabled) return '已停止'
  return row.status === 'ready'
    ? '运行中'
    : row.status === 'pending'
      ? '待部署'
      : row.status === 'error'
        ? '异常'
        : row.status
}
function appPort(row: Resource) {
  if (row.config.type === 'managed') return row.config.port || '—'
  return panel.resources.find((r) => r.id === row.config.legacyAppId)?.config.port || '—'
}
async function save() {
  if (!selected.value || saving.value) return
  try {
    saving.value = true
    error.value = ''
    const queued = await post('/sites/' + selected.value.id + '/settings', {
      name: draftName.value,
      config: draft.value,
      revision: selected.value.revision,
    })
    while (!reads.signal.aborted) {
      const task = await api('/tasks/' + queued.taskId, { signal: reads.signal })
      if (task.state === 'succeeded') break
      if (['failed', 'cancelled', 'interrupted'].includes(task.state))
        throw new Error(task.error || '应用未完成，草稿已保留')
      await new Promise((resolve) => setTimeout(resolve, 800))
    }
    if (reads.signal.aborted) return
    baseline.value = JSON.stringify([draftName.value, draft.value])
    settingsOpen.value = false
    await panel.refresh(['resources', 'tasks'])
    ElMessage.success('配置已应用')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    saving.value = false
  }
}
async function retry(row: Resource) {
  try {
    await post('/sites/' + row.id + '/provision')
    await panel.refresh(['tasks'])
    ElMessage.success('重试任务已提交')
  } catch (e) {
    error.value = errorText(e)
  }
}
async function run(row: Resource, op: string, params: Config = {}) {
  try {
    await ElMessageBox.confirm(
      `确认对 ${row.name} 执行${op === 'delete' ? '删除配置并保留文件' : op === 'stop' ? '停止访问' : op === 'resume' ? '恢复访问' : op === 'renew' ? '申请 / 续期证书' : '操作'}？`,
      '网站管理',
      { confirmButtonText: '确认', cancelButtonText: '取消' },
    )
    await action(row.id, op, params)
    await panel.refresh()
    ElMessage.success('任务已提交')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
async function loadLogs() {
  if (!selected.value) return
  try {
    logs.value =
      (
        await api(
          `/sites/${selected.value.id}/${page.value === 'process' ? 'process-logs' : 'logs'}`,
        )
      ).content || '暂无日志'
  } catch (e) {
    error.value = errorText(e)
  }
}
</script>
<template>
  <el-alert
    v-if="route.query.retired"
    title="应用进程功能已下线，已有服务与数据保留；关联网站继续作为外部应用访问。"
    type="info"
    :closable="false"
  />
  <el-tabs v-model="tab">
    <el-tab-pane label="PHP 项目" name="php" />
    <el-tab-pane label="反向代理" name="proxy" />
    <el-tab-pane label="其他项目" name="other" />
  </el-tabs>
  <LoadingContent :busy="panel.pending.resources">
    <div v-if="!nginx.length" class="surface nginx-required">
      <h2>安装 Nginx，开始管理网站</h2>
      <p>网站需要受管的 Nginx 入口。已有外部服务请先检查端口与配置冲突。</p>
      <el-button type="primary" @click="$router.push('/software')">前往软件市场安装</el-button>
    </div>
    <template v-else>
      <div class="site-toolbar">
        <el-button type="primary" @click="openCreate">
          添加{{ tab === 'php' ? 'PHP 项目' : tab === 'proxy' ? '反向代理' : '项目' }}
        </el-button>
        <SiteTools
          :nginx="nginx"
          @settings="
            (row, key) => {
              open(row)
              page = key
            }
          "
        />
        <el-select v-model="category" placeholder="全部分类" clearable style="width: 140px">
          <el-option v-for="v in categories" :key="v" :label="v" :value="v" />
        </el-select>
        <el-button @click="categoryOpen = true">分类管理</el-button>
        <el-input v-model="filter" placeholder="搜索名称、域名或备注" clearable />
        <span>{{ rows.length }} 个项目</span>
      </div>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <div class="surface site-table">
        <DataList
          :key="'Sites-1' + ':' + tab"
          :state-key="'Sites-1' + ':' + tab"
          :rows="rows"
          row-key="id"
          @row-dblclick="open"
        >
          <el-table-column
            :label="tab === 'php' ? '网站名' : tab === 'proxy' ? '域名' : '项目名称'"
            min-width="150"
          >
            <template #default="{ row }">
              <el-button link type="primary" @click="open(row)">
                {{ tab === 'proxy' ? row.config.domains : row.name }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="95">
            <template #default="{ row }">
              <el-tag
                :type="status(row) === '运行中' ? 'success' : 'info'"
                @click="run(row, status(row) === '运行中' ? 'stop' : 'resume')"
              >
                {{ status(row) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column v-if="tab === 'php'" label="PHP" width="100">
            <template #default="{ row }">
              <el-button
                link
                @click="
                  () => {
                    open(row)
                    page = 'runtime'
                  }
                "
              >
                {{ phpLabel(row) }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column v-if="tab === 'php'" label="备份" width="120">
            <template #default="{ row }">
              <el-button
                link
                @click="
                  () => {
                    open(row)
                    page = 'backup'
                  }
                "
              >
                {{ backupSummary(row) }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column
            v-if="tab === 'proxy'"
            label="代理地址"
            min-width="180"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ row.config.upstream }}</template>
          </el-table-column>
          <el-table-column v-if="tab === 'other'" label="运行端口" width="100">
            <template #default="{ row }">{{ appPort(row) }}</template>
          </el-table-column>
          <el-table-column
            v-if="tab !== 'proxy'"
            label="根目录"
            min-width="190"
            show-overflow-tooltip
          >
            <template #default="{ row }">
              <el-button
                link
                type="primary"
                :disabled="!row.config.documentRoot"
                @click="$router.push({ path: '/files', query: { path: row.config.documentRoot } })"
              >
                {{ row.config.documentRoot || '部署后生成' }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column v-if="tab !== 'other'" label="日流量" width="110">
            <template #default="{ row }">
              <el-button
                link
                @click="
                  () => {
                    open(row)
                    page = 'traffic'
                  }
                "
              >
                {{ traffic[row.id] ? size(traffic[row.id].todayBytes) : '查看' }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column v-if="tab !== 'proxy'" label="到期时间" width="155">
            <template #default="{ row }">
              <el-button
                link
                @click="
                  () => {
                    open(row)
                    page = 'expiry'
                  }
                "
              >
                {{ row.config.expiresAt ? date(row.config.expiresAt) : '永久有效' }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column label="备注" min-width="110" show-overflow-tooltip>
            <template #default="{ row }">
              <el-button
                link
                @click="
                  () => {
                    open(row)
                    page = 'note'
                  }
                "
              >
                {{ row.config.note || '添加备注' }}
              </el-button>
            </template>
          </el-table-column>

          <el-table-column label="SSL证书" width="150">
            <template #default="{ row }">
              <el-button
                link
                :type="row.config.tls ? 'success' : 'info'"
                @click="
                  () => {
                    open(row)
                    page = 'ssl'
                  }
                "
              >
                {{ row.config.certificateExpires ? date(row.config.certificateExpires) : '未部署' }}
              </el-button>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="170" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="open(row)">设置</el-button>
              <el-button v-if="['error', 'pending'].includes(row.status)" link @click="retry(row)">
                重试
              </el-button>
              <el-button link type="danger" @click="run(row, 'delete')">删除</el-button>
            </template>
          </el-table-column>
        </DataList>
      </div>
    </template>
  </LoadingContent>
  <SiteCreate v-model="createOpen" :initial="initial" />
  <AppDialog
    v-model="settingsOpen"
    :before-close="closeSettings"
    :title="selected?.name + ' · 网站设置'"
    split
    width="min(1000px,95vw)"
    :close-on-click-modal="false"
  >
    <div class="site-settings ops-settings-layout" :inert="saving || undefined" :aria-busy="saving">
      <aside>
        <button
          v-for="[key, label] in [
            ['general', '域名与基础'],
            ...(localSite
              ? [
                  ['directory', '网站目录'],
                  ['runtime', '运行环境'],
                  ['routing', '默认文档与伪静态'],
                ]
              : [['runtime', draft.type === 'proxy' ? '反向代理' : '进程配置']]),
            ['advanced', '高级配置'],
            ['security', '网站安全'],
            ['ssl', 'SSL 证书'],
            ['traffic', '流量统计'],
            ['expiry', '到期时间'],
            ['note', '备注'],
            ['logs', '网站日志'],
            ...(selected?.config.type === 'managed' ? [['process', '应用进程']] : []),
            ['backup', '网站备份'],
          ]"
          :key="key"
          :class="{ active: page === key }"
          @click="switchPage(key)"
        >
          {{ label }}
        </button>
      </aside>
      <section class="ops-settings-content">
        <el-alert v-if="error" :title="error" type="error" :closable="false" />
        <template v-if="page === 'general'">
          <el-form label-width="110px">
            <el-form-item label="项目名称">
              <el-input v-model="draftName" maxlength="63" />
            </el-form-item>
            <el-form-item label="绑定域名">
              <el-input v-model="draft.domains" type="textarea" :rows="4" />
            </el-form-item>
            <el-form-item label="网站分类">
              <el-select v-model="draft.category" placeholder="默认分类">
                <el-option v-for="v in categories" :key="v" :value="v" :label="v" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="draft.databaseId" label="关联数据库">
              <span>
                {{
                  panel.resources.find((r) => r.id === draft.databaseId)?.name || draft.databaseId
                }}
              </span>
              <el-button link @click="$router.push('/databases')">管理数据库</el-button>
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'directory'">
          <el-form label-width="110px">
            <el-form-item label="根目录">
              <el-input :model-value="draft.documentRoot || draft.rootDirectory" readonly />
            </el-form-item>
            <el-form-item label="运行目录">
              <el-input
                v-model="draft.runningDirectory"
                placeholder="相对网站根目录，例如 public"
              />
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'routing'">
          <el-form label-width="150px"><SiteRuntimeOptions v-model="draft" /></el-form>
        </template>
        <template v-else-if="page === 'runtime'">
          <el-form label-width="110px">
            <el-form-item v-if="localSite" label="PHP 版本">
              <el-select v-model="runtime">
                <el-option label="静态" value="static" />
                <el-option
                  v-for="r in panel
                    .kind('instance')
                    .filter(
                      (r) =>
                        r.config.software === 'php' &&
                        r.config.mode === 'native' &&
                        ['ready', 'stopped', 'running'].includes(r.status),
                    )"
                  :key="r.id"
                  :value="r.id"
                  :label="'PHP ' + r.config.version"
                />
              </el-select>
            </el-form-item>
            <template v-if="['proxy', 'app'].includes(draft.type)">
              <el-form-item label="目标地址"><el-input v-model="draft.upstream" /></el-form-item>
              <el-form-item label="发送 Host"><el-input v-model="draft.proxyHost" /></el-form-item>
              <SiteRuntimeOptions v-model="draft" />
            </template>
            <template v-if="draft.type === 'managed'">
              <el-form-item label="执行文件"><el-input v-model="draft.executable" /></el-form-item>
              <el-form-item label="启动参数">
                <el-input v-model="draft.arguments" placeholder="JSON 数组" />
              </el-form-item>
              <el-form-item label="运行端口">
                <el-input-number v-model="draft.port" :min="1024" :max="65535" />
              </el-form-item>
              <el-form-item label="运行用户">
                <el-select v-model="draft.user">
                  <el-option label="独立站点用户" value="isolated" />
                  <el-option label="www-data" value="www-data" />
                </el-select>
              </el-form-item>
              <el-form-item label="环境变量">
                <el-input
                  v-model="draft.environment"
                  type="textarea"
                  :rows="4"
                  placeholder="JSON 对象"
                />
              </el-form-item>
              <el-form-item label="健康检查"><el-input v-model="draft.healthURL" /></el-form-item>
              <el-form-item label="开机启动"><el-switch v-model="draft.autoStart" /></el-form-item>
            </template>
          </el-form>
        </template>
        <template v-else-if="page === 'advanced'">
          <el-form label-position="top">
            <el-form-item label="Nginx 高级参数">
              <CodeEditor v-model="draft.advanced" />
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'security'">
          <el-form label-width="150px">
            <el-form-item label="安全响应头">
              <el-switch v-model="draft.securityHeaders" />
            </el-form-item>
            <el-form-item label="禁止嵌入框架">
              <el-switch v-model="draft.denyFrames" />
            </el-form-item>
            <el-form-item label="禁止上传目录执行 PHP">
              <el-switch v-model="draft.denyUploadPHP" />
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'ssl'">
          <p>当前证书：{{ date(selected?.config.certificateExpires) }}</p>
          <el-form label-position="top">
            <el-form-item label="ACME 邮箱"><el-input v-model="draft.email" /></el-form-item>
            <el-checkbox v-model="draft.acceptACMETerms">同意所选 ACME 服务条款</el-checkbox>
            <el-form-item label="HTTPS 跳转">
              <el-switch v-model="draft.redirectHTTPS" />
            </el-form-item>
          </el-form>
          <DialogActions><el-button @click="save">保存申请设置</el-button></DialogActions>
          <DialogActions>
            <el-button v-if="selected" type="primary" @click="run(selected, 'renew')">
              免费申请 / 续期
            </el-button>
          </DialogActions>
          <el-divider />
          <el-input v-model="cert.certificate" type="textarea" placeholder="PEM 证书链" :rows="4" />
          <el-input v-model="cert.privateKey" type="textarea" placeholder="PEM 私钥" :rows="4" />
          <DialogActions>
            <el-button v-if="selected" @click="run(selected, 'certificate', cert)">
              验证并上传证书
            </el-button>
          </DialogActions>
        </template>
        <template v-else-if="page === 'traffic'">
          <h3>今日流量 {{ size(traffic[selected?.id || '']?.todayBytes) }}</h3>
          <p>HTTP 响应字节 · 服务器时区 {{ traffic[selected?.id || '']?.timezone || '—' }}</p>
          <el-radio-group v-model="trafficPeriod">
            <el-radio-button value="24h">24 小时</el-radio-button>
            <el-radio-button value="7d">7 日</el-radio-button>
            <el-radio-button value="30d">30 日</el-radio-button>
          </el-radio-group>
          <div class="traffic-bars">
            <div v-for="point in chart" :key="point.hour">
              <span>{{ point.hour.replace('T', ' ') }}</span>
              <meter :value="point.bytes" :max="Math.max(...chart.map((p) => p.bytes), 1)" />
              <small>{{ size(point.bytes) }}</small>
            </div>
          </div>
          <el-button v-if="selected" @click="loadTraffic(selected)">刷新统计</el-button>
        </template>
        <template v-else-if="page === 'expiry'">
          <p>到期停止公众访问及专属进程，文件与数据库保留。延长时间后点击恢复访问。</p>
          <el-date-picker
            v-model="draft.expiresAt"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
            placeholder="永久有效"
            clearable
          />
          <DialogActions>
            <el-button type="primary" @click="save">保存到期时间</el-button>
          </DialogActions>
          <DialogActions>
            <el-button v-if="selected" @click="run(selected, 'resume')">恢复访问</el-button>
          </DialogActions>
        </template>
        <template v-else-if="page === 'note'">
          <el-input v-model="draft.note" type="textarea" maxlength="1000" :rows="6" />
          <DialogActions>
            <el-button type="primary" @click="save">保存备注</el-button>
          </DialogActions>
        </template>
        <template v-else-if="page === 'process'">
          <p>执行文件：{{ selected?.config.executable }}</p>
          <p>
            端口：{{ selected?.config.port }} · 开机启动：{{
              selected?.config.autoStart ? '开启' : '关闭'
            }}
          </p>
          <DialogActions>
            <el-button
              v-if="selected"
              @click="
                () => {
                  page = 'runtime'
                }
              "
            >
              编辑进程配置
            </el-button>
          </DialogActions>
          <DialogActions>
            <el-button v-if="selected" @click="run(selected, 'apply')">重新部署进程</el-button>
          </DialogActions>
          <el-button @click="loadLogs">读取进程日志</el-button>
          <pre>{{ logs }}</pre>
        </template>
        <template v-else-if="page === 'logs'">
          <el-button @click="loadLogs">刷新日志</el-button>
          <pre>{{ logs }}</pre>
        </template>
        <template v-else-if="page === 'backup'">
          <p v-for="b in relatedBackups" :key="b.id">
            {{ b.name }} · {{ b.status }}
            <el-button link @click="run(b, 'backup')">立即备份</el-button>
          </p>
          <DialogActions>
            <el-button
              @click="
                () => {
                  $router.push('/backups')
                  settingsOpen = false
                }
              "
            >
              创建备份与计划
            </el-button>
          </DialogActions>
        </template>
      </section>
    </div>
    <template #footer>
      <el-button :disabled="saving" @click="closeSettings(() => (settingsOpen = false))">
        关闭
      </el-button>
      <el-button
        v-if="['general', 'directory', 'runtime', 'routing', 'advanced', 'security'].includes(page)"
        type="primary"
        :loading="saving"
        @click="save"
      >
        保存并应用
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="categoryOpen" title="网站分类" size="small" width="520px">
    <div class="page-toolbar">
      <el-input v-model="categoryName" placeholder="分类名称" />
      <el-button type="primary" @click="changeCategory()">
        {{ categoryOld ? '保存改名' : '添加分类' }}
      </el-button>
    </div>
    <el-table :data="categories.map((name) => ({ name }))">
      <el-table-column prop="name" label="分类" />
      <el-table-column label="操作">
        <template #default="{ row }">
          <template v-if="row.name !== '默认分类'">
            <el-button
              link
              @click="
                () => {
                  categoryOld = row.name
                  categoryName = row.name
                }
              "
            >
              改名
            </el-button>
            <el-button
              link
              type="danger"
              @click="
                () => {
                  categoryOld = row.name
                  changeCategory(true)
                }
              "
            >
              删除
            </el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>
  </AppDialog>
</template>
<style scoped>
.site-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 12px 0;
}
.site-toolbar .el-input {
  width: 230px;
  margin-left: auto;
}
.site-toolbar > span {
  margin-left: auto;
  color: var(--el-text-color-secondary);
}
.site-table {
  padding: 12px;
}
.nginx-required {
  text-align: center;
  padding: 70px 20px;
}
.nginx-required p {
  color: var(--el-text-color-secondary);
}
.site-settings pre {
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--el-fill-color-light);
  padding: 12px;
}
.traffic-bars > div {
  display: flex;
  gap: 14px;
  align-items: center;
  margin: 8px 0;
}
.traffic-bars meter {
  flex: 1;
}
.traffic-bars span {
  font-size: 12px;
}
.traffic-bars small {
  width: 75px;
}
</style>
