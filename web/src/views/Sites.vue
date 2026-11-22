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
import SiteRuntimeFields from '../components/SiteRuntimeFields.vue'
const runtimeFields = ref<InstanceType<typeof SiteRuntimeFields>>()
import SiteDelete from '../components/SiteDelete.vue'
import { Refresh } from '@element-plus/icons-vue'
import SiteTools from '../components/SiteTools.vue'
import SiteRuntimeOptions from '../components/SiteRuntimeOptions.vue'
import SiteDomainManager from '../components/SiteDomainManager.vue'
import SitePolicyFields from '../components/SitePolicyFields.vue'
import SiteConfigViewer from '../components/SiteConfigViewer.vue'
import SiteAlerts from '../components/SiteAlerts.vue'
import SiteSafety from '../components/SiteSafety.vue'
import { observeSiteTask } from '../lib/siteTask'
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
  logKind = ref('access'),
  directories = ref<string[]>([]),
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
const checked = ref<Resource[]>([]),
  deleteOpen = ref(false),
  deleteRows = ref<Resource[]>([]),
  refreshing = ref(false),
  list = ref<any>()
watch([tab, filter, category], () => {
  checked.value = []
  list.value?.clearSelection?.()
})
async function refreshSites() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await panel.refresh(['resources', 'tasks'])
    await Promise.all(rows.value.map(loadTraffic))
    if (panel.errors.resources) error.value = panel.errors.resources
  } finally {
    refreshing.value = false
  }
}
function removeSites(items: Resource[]) {
  deleteRows.value = items
  deleteOpen.value = true
}
async function deleted() {
  checked.value = []
  list.value?.clearSelection?.()
  await panel.refresh(['resources', 'tasks'])
}
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
  if (key === 'subdirectories') await loadDirectories()
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
  directories.value = []
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
      ? '创建中'
      : row.status === 'error'
        ? '创建失败'
        : row.status
}
function appPort(row: Resource) {
  if (row.config.type === 'managed') return row.config.port || '—'
  return panel.resources.find((r) => r.id === row.config.legacyAppId)?.config.port || '—'
}
async function save(): Promise<boolean> {
  if (!selected.value || saving.value) return false
  const id = selected.value.id
  try {
    saving.value = true
    error.value = ''
    const queued = await api('/sites/' + id + '/settings', {
      method: 'POST',
      headers: runtimeFields.value?.headers() || {},
      body: JSON.stringify({
        name: draftName.value,
        config: draft.value,
        revision: selected.value.revision,
      }),
    })
    const task = await observeSiteTask(queued.taskId, reads.signal)
    if (task.state !== 'succeeded') throw new Error(task.error || '应用未完成，草稿已保留')
    await panel.refresh(['resources', 'tasks'])
    if (panel.errors.resources)
      throw new Error('配置已应用，列表更新失败：' + panel.errors.resources + '；请刷新后核对')
    const current = panel.resources.find((r) => r.id === id)
    if (!current) throw new Error('配置已应用，站点记录暂不可查询；请刷新后核对')
    selected.value = current
    draft.value = JSON.parse(JSON.stringify(current.config))
    draftName.value = current.name
    baseline.value = JSON.stringify([draftName.value, draft.value])
    ElMessage.success('配置已应用')
    return true
  } catch (e) {
    if (!isAbort(e)) error.value = errorText(e)
    return false
  } finally {
    runtimeFields.value?.clearCredentials()
    saving.value = false
  }
}
async function loadDirectories() {
  const id = selected.value?.id
  if (!id) return
  try {
    const result = await api(`/sites/${id}/directories`, { signal: reads.signal })
    if (selected.value?.id === id) directories.value = result.directories || []
  } catch (e) {
    if (!isAbort(e) && selected.value?.id === id) error.value = errorText(e)
  }
}
async function run(row: Resource, op: string, params: Config = {}) {
  try {
    await ElMessageBox.confirm(
      `确认对 ${row.name} 执行${op === 'stop' ? '停止访问' : op === 'resume' ? '恢复访问' : op === 'renew' ? '申请 / 续期证书' : '操作'}？`,
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
let logSequence = 0
async function loadLogs() {
  const id = selected.value?.id
  if (!id) return
  const sequence = ++logSequence
  logs.value = ''
  try {
    const result = await api(
      `/sites/${id}/${page.value === 'process' ? 'process-logs' : 'logs?kind=' + logKind.value}`,
      { signal: reads.signal },
    )
    if (
      sequence === logSequence &&
      selected.value?.id === id &&
      ['logs', 'process'].includes(page.value)
    )
      logs.value = result.content || '暂无日志'
  } catch (e) {
    if (!isAbort(e) && sequence === logSequence && selected.value?.id === id)
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
  <LoadingContent :busy="panel.pending.resources" :ready="panel.hasData.resources">
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
        <el-button :disabled="!checked.length" type="danger" plain @click="removeSites(checked)">
          批量删除
          <span v-if="checked.length">（{{ checked.length }}）</span>
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
        <div class="site-search">
          <el-input v-model="filter" placeholder="搜索名称、域名或备注" clearable />
          <el-button
            :icon="Refresh"
            :loading="refreshing"
            aria-label="刷新网站列表"
            title="刷新网站列表"
            @click="refreshSites"
          />
        </div>
      </div>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <div class="surface site-table">
        <DataList
          :key="'Sites-1' + ':' + tab"
          :state-key="'Sites-1' + ':' + tab"
          :rows="rows"
          ref="list"
          @selection-change="(items: Resource[]) => (checked = items)"
          row-key="id"
          @row-dblclick="open"
        >
          <el-table-column type="selection" width="44" reserve-selection />
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
              <el-button link type="danger" @click="removeSites([row])">删除</el-button>
            </template>
          </el-table-column>
        </DataList>
      </div>
    </template>
  </LoadingContent>
  <SiteCreate v-model="createOpen" :initial="initial" />
  <SiteDelete v-model="deleteOpen" :rows="deleteRows" @completed="deleted" />
  <AppDialog
    v-model="settingsOpen"
    :before-close="closeSettings"
    :title="selected?.name + ' · 网站设置'"
    split
    class="site-settings-dialog"
    width="min(1000px,95vw)"
    :close-on-click-modal="false"
  >
    <div class="site-settings ops-settings-layout" :inert="saving || undefined" :aria-busy="saving">
      <aside>
        <button
          v-for="[key, label] in [
            ['general', '域名管理'],
            ...(localSite
              ? [
                  ['subdirectories', '子目录绑定'],
                  ['directory', '网站目录'],
                ]
              : []),
            ['access', '访问限制'],
            ['limits', '流量限制'],
            ...(localSite
              ? [
                  ['routing', '伪静态'],
                  ['documents', '默认文档'],
                ]
              : []),
            ['advanced', '配置文件'],
            ['ssl', 'SSL 证书'],
            ['runtime', localSite ? 'PHP' : draft.type === 'proxy' ? '反向代理' : '进程配置'],
            ['redirects', '重定向'],
            ...(localSite ? [['proxy-rules', '反向代理']] : []),
            ['hotlink', '防盗链'],
            ['tamper', '防篡改'],
            ['security', '网站安全'],
            ['logs', '网站日志'],
            ['alerts', '网站告警'],
            ['other', '其他设置'],
            ['traffic', '流量统计'],
            ['expiry', '到期时间'],
            ['note', '分类与备注'],
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
        <template v-if="page === 'general' || page === 'subdirectories'">
          <SiteDomainManager
            v-if="selected"
            :key="selected.id + page"
            v-model="draft"
            :committed="selected.config"
            :site-id="selected.id"
            :port="
              panel.resources.find((r) => r.id === selected?.config.instanceId)?.config.port || 80
            "
            :busy="saving"
            :child="page === 'subdirectories'"
            :directories="directories"
            :apply="save"
          />
        </template>
        <template
          v-else-if="
            ['access', 'limits', 'redirects', 'proxy-rules', 'hotlink', 'other'].includes(page)
          "
        >
          <SitePolicyFields v-model="draft" :key="selected?.id + page" :page="page" />
        </template>
        <template v-else-if="page === 'alerts' && selected">
          <SiteAlerts :key="selected.id" :site="selected" />
        </template>
        <template v-else-if="page === 'tamper'">
          <el-alert title="当前未集成内核防篡改执行器" type="info" :closable="false" />
          <p class="form-help">
            宝塔此项依赖付费内核插件。普通目录权限不能替代内核防篡改；本版本不提供无法验证的开关。可在文件管理中核对权限，并配置独立备份。
          </p>
          <el-button @click="page = 'backup'">查看网站备份</el-button>
        </template>
        <template v-else-if="page === 'directory'">
          <el-form label-width="104px">
            <el-form-item label="根目录">
              <el-input :model-value="draft.documentRoot || draft.rootDirectory" readonly />
              <small class="form-help">根目录不在此移动，避免目录与站点状态不一致。</small>
              <el-button
                link
                @click="
                  $router.push({
                    path: '/files',
                    query: { path: draft.documentRoot || draft.rootDirectory },
                  })
                "
              >
                打开文件管理
              </el-button>
            </el-form-item>
            <el-form-item label="运行目录">
              <el-input
                v-model="draft.runningDirectory"
                placeholder="相对网站根目录，例如 public"
              />
            </el-form-item>
            <el-form-item label="访问日志">
              <el-switch
                :model-value="!draft.accessLogDisabled"
                @update:model-value="draft.accessLogDisabled = !$event"
              />
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'routing'">
          <el-form label-width="104px">
            <el-form-item label="伪静态模板">
              <el-select v-model="draft.rewriteTemplate" placeholder="默认路由规则">
                <el-option label="直接文件访问" value="none" />
                <el-option v-if="draft.type === 'static'" label="单页应用" value="spa" />
                <template v-else>
                  <el-option
                    v-for="[value, label] in [
                      ['front-controller', 'PHP 前端控制器'],
                      ['wordpress', 'WordPress'],
                      ['thinkphp', 'ThinkPHP'],
                      ['laravel', 'Laravel（运行目录 public）'],
                    ]"
                    :key="value"
                    :value="value"
                    :label="label"
                  />
                </template>
              </el-select>
            </el-form-item>
          </el-form>
          <p class="form-help">
            模板生成受控路由，应用时检查 Nginx 配置；Laravel 请在网站目录中设置运行目录 public。
          </p>
        </template>
        <template v-else-if="page === 'documents'">
          <el-form label-position="top">
            <el-form-item label="默认文档（每行一个，按顺序查找）">
              <el-input
                v-model="draft.defaultDocuments"
                type="textarea"
                :rows="8"
                :placeholder="draft.type === 'php' ? 'index.php\nindex.html' : 'index.html'"
              />
            </el-form-item>
          </el-form>
        </template>
        <template v-else-if="page === 'runtime'">
          <el-form label-width="104px">
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
            <template v-if="draft.type === 'php'">
              <el-form-item label="目录隔离">
                <el-switch v-model="draft.phpIsolation" />
                <small class="form-help">限制 PHP 读取项目、会话及临时目录，保存后生效。</small>
              </el-form-item>
              <el-form-item label="上传禁用 PHP">
                <el-switch v-model="draft.denyUploadPHP" />
              </el-form-item>
              <p class="form-help">
                每站点独立 FPM 用户和文件 Session 目录；Redis/Memcached Session
                沿用已配置的站点命名空间。
              </p>
            </template>
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
              <SiteRuntimeFields ref="runtimeFields" v-model="draft" :site="selected" />
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
        <template v-else-if="page === 'advanced' && selected">
          <SiteConfigViewer
            :key="selected.id + '-' + selected.revision"
            v-model="draft"
            :site-id="selected.id"
          />
        </template>
        <template v-else-if="page === 'security' && selected">
          <SiteSafety :key="selected.id" :site-id="selected.id" />
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
          <el-form label-width="104px">
            <el-form-item label="项目名称">
              <el-input v-model="draftName" maxlength="63" />
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
          <div class="page-toolbar">
            <el-radio-group v-model="logKind" @change="loadLogs">
              <el-radio-button value="access">访问日志</el-radio-button>
              <el-radio-button value="error">错误日志</el-radio-button>
            </el-radio-group>
            <el-button @click="loadLogs">刷新日志</el-button>
          </div>
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
        v-if="
          [
            'general',
            'subdirectories',
            'directory',
            'runtime',
            'routing',
            'documents',
            'advanced',
            'access',
            'limits',
            'redirects',
            'proxy-rules',
            'hotlink',
            'other',
          ].includes(page)
        "
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
.site-search {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  width: min(100%, 280px);
}
.site-search .el-input {
  flex: 1;
  min-width: 0;
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
