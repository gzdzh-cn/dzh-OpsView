<script setup lang="ts">
import LoadingContent from '../components/LoadingContent.vue'
import AppDialog from '../components/AppDialog.vue'
import { usePageState } from '../composables/pageState'
import PluginMarket from '../components/PluginMarket.vue'
import DataList from '../components/DataList.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  marketCategories,
  pluginCategory,
  pluginEdition,
  matchesMarketCategory,
} from '../lib/market'
const route = useRoute(),
  router = useRouter()
import { ElMessage, ElMessageBox } from 'element-plus'
import { Grid, MoreFilled } from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { post, action, errorText, type Config } from '../lib/api'
import ResourceForm from '../components/ResourceForm.vue'
import Resources from './Resources.vue'
import SoftwareSettings from '../components/SoftwareSettings.vue'
import type { Resource } from '../lib/api'
import CodeEditor from '../components/CodeEditor.vue'
const panel = usePanel(),
  tab = usePageState('market-tab', 'market'),
  category = usePageState('market-category', 'all'),
  pluginMarket = ref<InstanceType<typeof PluginMarket>>(),
  install = ref(false),
  initial = ref<Config>({}),
  importOpen = ref(false),
  importType = ref('plugin'),
  importName = ref(''),
  importText = ref(''),
  busy = ref(false)
watch(
  () => route.query.software,
  (value) => {
    if (value) {
      tab.value = 'market'
      category.value = 'runtime'
    }
  },
  { immediate: true },
)
const groups = computed(() =>
  ['nginx', 'mysql', 'php', 'redis', 'postgresql', 'nodejs'].map((software) => ({
    software,
    versions: panel.catalog.filter((v) => v.software === software),
    title:
      software === 'php'
        ? 'PHP'
        : software === 'redis'
          ? 'Redis'
          : software === 'nginx'
            ? 'Nginx'
            : software === 'mysql'
              ? 'MySQL'
              : software === 'nodejs'
                ? 'Node.js'
                : 'PostgreSQL',
    symbol:
      software === 'php'
        ? 'PHP'
        : software === 'redis'
          ? 'R'
          : software === 'nginx'
            ? 'N'
            : software === 'mysql'
              ? 'M'
              : software === 'nodejs'
                ? 'JS'
                : 'P',
    description:
      software === 'php'
        ? '多版本 PHP 与独立 FPM，为每个网站选择运行环境和扩展。'
        : software === 'redis'
          ? '高性能内存数据库与缓存，支持持久化、实例隔离和资源控制。'
          : software === 'nginx'
            ? '高性能 Web 服务与反向代理，连接域名、静态站点和你的应用。'
            : software === 'mysql'
              ? '为业务应用提供可靠的关系型数据存储，独立版本与实例管理。'
              : software === 'nodejs'
                ? '安装独立的 Node.js LTS 运行时，为应用选择准确版本的可执行文件。'
                : '功能丰富的开源关系型数据库，为复杂查询与数据应用提供支持。',
  })),
)
const marketRows = computed(() => {
  const plugins = pluginMarket.value?.plugins || []
  const rows: any[] = []
  if (category.value === 'installed') {
    for (const r of panel.kind('instance')) {
      const g = groups.value.find((g) => g.software === r.config.software)
      rows.push({
        key: r.id,
        kind: 'instance',
        title: g?.title || r.name,
        symbol: g?.symbol || 'A',
        description: g?.description || '已登记的软件实例',
        software: r.config.software,
        category: r.config.mode === 'docker' ? 'docker' : 'runtime',
        edition: 'free',
        installed: true,
        resource: r,
        version: r.config.version || '—',
      })
    }
    for (const r of panel.kind('plugin').filter((r) => r.status !== 'uninstalled')) {
      const p = plugins.find((p: any) => r.config.pluginId === p.id || r.name === p.id)
      rows.push(
        pluginRow(
          p || {
            id: r.config.pluginId || r.name,
            name: r.name,
            version: r.config.version,
            type: r.config.pluginType,
            description: '仓库中暂无此插件，仍可卸载已登记资源。',
            unavailable: true,
          },
          r,
        ),
      )
    }
  } else {
    for (const g of groups.value)
      rows.push({
        ...g,
        key: 'software-' + g.software,
        kind: 'software',
        category: 'runtime',
        edition: 'free',
        installed: panel.kind('instance').some((r) => r.config.software === g.software),
      })
    for (const p of plugins) rows.push(pluginRow(p))
  }
  return rows.filter(
    (row) =>
      matchesMarketCategory(row, category.value) &&
      (!route.query.software || row.software === route.query.software),
  )
})
function pluginRow(p: any, resource?: Resource) {
  const r =
    resource ||
    panel
      .kind('plugin')
      .find((r) => r.status !== 'uninstalled' && (r.config.pluginId === p.id || r.name === p.id))
  return {
    key: r?.id || 'plugin-' + p.id,
    kind: 'plugin',
    title: p.name,
    symbol: p.type === 'application' ? 'D' : 'P',
    description: p.description,
    category: pluginCategory(p),
    edition: pluginEdition(p),
    installed: !!r,
    resource: r,
    plugin: p,
    version: r?.config.version || p.version || '—',
  }
}
function showAll(navigate: () => unknown) {
  category.value = 'all'
  void navigate()
}
function selectCategory(value: string) {
  category.value = value
  if (route.query.software)
    void router.replace({ path: '/software', query: { ...route.query, software: undefined } })
}
function rowResource(row: any): Resource | undefined {
  return row.kind === 'software' ? selectedInstance(row.software) : row.resource
}
function rowStatus(row: any) {
  const r = rowResource(row)
  if (!r) return '未安装'
  if (r.status === 'ready' && (row.kind === 'plugin' || row.software === 'nodejs')) return '已安装'
  const statuses: Record<string, string> = {
    ready: '运行中',
    running: '运行中',
    stopped: '已停止',
    pending: '等待中',
    installing: '安装中',
    error: '异常',
    uninstalled: '待清理',
  }
  return statuses[r.status] || r.status
}
function editRow(row: any) {
  if (row.kind === 'plugin') pluginMarket.value?.openPlugin(row.plugin)
  else if (row.kind === 'instance') chooseInstance(row.resource)
  else openSettings(row.software)
}
async function removeRow(row: any) {
  if (row.kind === 'plugin') await pluginMarket.value?.removePlugin(row.plugin)
  else if (row.kind === 'instance') await removeInstance(row.resource)
  else await uninstallSoftware(row.software)
}
async function instanceAction(row: any, command: string) {
  const r = rowResource(row)
  if (!r) return
  try {
    await ElMessageBox.confirm(
      `对 ${r.name} 执行${{ start: '启动', stop: '停止', restart: '重启' }[command]}？关联业务可能受影响。`,
      '服务操作',
      { confirmButtonText: '执行', cancelButtonText: '取消' },
    )
    await action(r.id, command)
    await panel.refresh(['resources', 'tasks'])
    ElMessage.success('操作已提交任务中心')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
const versions = ref<Record<string, string>>({})
const chosenVersions = new Set<string>()
function selectedInstance(software: string) {
  const entry = panel.catalog.find((v) => v.id === versions.value[software])
  return panel
    .kind('instance')
    .find((r) => r.config.software === software && r.config.version === entry?.version)
}
function installationLabel(r: Resource) {
  if (r.status === 'uninstalled') return '待清理'
  if (r.status === 'error') return '安装异常'
  if (['pending', 'installing'].includes(r.status)) return '已占用'
  return '已安装'
}
const settingsOpen = ref(false),
  settingsResource = ref<Resource>(),
  chooseOpen = ref(false),
  chosenSoftware = ref(''),
  introduction = ref<any>()
const matchingInstances = computed(() =>
  panel.kind('instance').filter((r) => r.config.software === chosenSoftware.value),
)
function openSettings(software: string) {
  chosenSoftware.value = software
  const items = matchingInstances.value.filter((r) => r.status !== 'uninstalled')
  if (items.length === 1) {
    settingsResource.value = items[0]
    settingsOpen.value = true
  } else chooseOpen.value = true
}
function chooseInstance(r: Resource) {
  chooseOpen.value = false
  settingsResource.value = r
  settingsOpen.value = true
}
async function uninstallSoftware(software: string) {
  chosenSoftware.value = software
  chooseOpen.value = true
}

async function removeInstance(r: Resource) {
  try {
    await ElMessageBox.confirm(
      r.status === 'uninstalled'
        ? `清理 ${r.name} 的记录以释放版本占用，原业务数据保留。`
        : `卸载 ${r.name}，保留业务数据。有关联资源时将拒绝卸载。`,
      '卸载实例',
      { confirmButtonText: '卸载', cancelButtonText: '取消' },
    )
    if (r.status === 'uninstalled')
      await import('../lib/api').then(({ api }) => api(`/resources/${r.id}`, { method: 'DELETE' }))
    else await action(r.id, 'uninstall')
    chooseOpen.value = false
    await panel.refresh(['resources', 'tasks', 'catalog'])
    ElMessage.success('卸载任务已提交')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
function openImport(type: string) {
  importType.value = type
  importOpen.value = true
}
watch(
  [groups, () => panel.resources],
  ([items]) => {
    for (const g of items) {
      if (
        chosenVersions.has(g.software) &&
        g.versions.some((v) => v.id === versions.value[g.software])
      )
        continue
      const occupied = panel.kind('instance').filter((r) => r.config.software === g.software)
      const existing =
        g.versions.find((v) =>
          occupied.some((r) => r.config.version === v.version && r.status !== 'uninstalled'),
        ) || g.versions.find((v) => occupied.some((r) => r.config.version === v.version))
      versions.value[g.software] =
        (
          existing ||
          g.versions.find((v) => v.recommended && !v.disabledReason) ||
          g.versions.find((v) => !v.disabledReason) ||
          g.versions[0]
        )?.id || ''
    }
  },
  { immediate: true },
)
function openInstall(software: string) {
  const entry =
    panel.catalog.find((v) => v.id === versions.value[software]) ||
    panel.catalog.find((v) => v.software === software && !v.disabledReason)
  initial.value = {
    catalogId: entry?.id,
    mode: 'native',
    installMethod:
      entry?.methods?.find((m: string) => !entry?.availability?.[m]) ||
      entry?.methods?.[0] ||
      (['mysql', 'nodejs'].includes(software) ? 'binary' : 'source'),
    softwareFilter: software,
    port:
      software === 'php'
        ? 9000
        : software === 'redis'
          ? 6379
          : software === 'mysql'
            ? 3306
            : software === 'postgresql'
              ? 5432
              : software === 'nodejs'
                ? 0
                : 80,
  }
  install.value = true
}
async function importPackage() {
  busy.value = true
  try {
    const envelope = JSON.parse(importText.value)
    if (importType.value === 'catalog') {
      await post('/catalog/import', envelope)
      await panel.refresh(['resources', 'tasks', 'catalog'])
    } else {
      const resource = await post('/plugins/import', { name: importName.value, envelope })
      await action(resource.id, 'install')
      await panel.refresh(['resources', 'tasks', 'catalog'])
    }
    importOpen.value = false
    ElMessage.success('已导入，签名由本机执行器验证')
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="插件市场" name="market" />
    <el-tab-pane label="仓库管理" name="repositories" />
  </el-tabs>
  <div v-if="tab === 'market'" class="surface software-list">
    <div class="page-toolbar market-toolbar">
      <span v-if="route.query.software" class="filter-label">
        当前仅显示
        {{ groups.find((g) => g.software === route.query.software)?.title || route.query.software }}
      </span>
      <router-link v-if="route.query.software" to="/software" custom v-slot="{ navigate, href }">
        <el-button type="primary" :icon="Grid" :data-href="href" @click="showAll(navigate)">
          显示全部软件
        </el-button>
      </router-link>
      <div class="market-tools">
        <el-dropdown trigger="click">
          <el-button :icon="MoreFilled" aria-label="市场更多操作">更多</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="openImport('catalog')">导入安装配方</el-dropdown-item>
              <el-dropdown-item @click="pluginMarket?.openImport()">导入签名插件</el-dropdown-item>
              <el-dropdown-item @click="pluginMarket?.load()">更新插件列表</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
    <div class="market-categories" role="group" aria-label="应用分类">
      <span class="category-label">应用分类</span>
      <el-button
        v-for="item in marketCategories"
        :key="item.id"
        :type="category === item.id ? 'primary' : 'default'"
        :text="category !== item.id"
        :aria-pressed="category === item.id"
        @click="selectCategory(item.id)"
      >
        {{ item.label }}
      </el-button>
    </div>
    <el-alert v-if="pluginMarket?.error" :title="pluginMarket.error" type="error" :closable="false">
      <el-button link @click="pluginMarket?.load()">重试插件列表</el-button>
    </el-alert>
    <LoadingContent
      :busy="
        panel.pending.catalog ||
        panel.pending.resources ||
        (!['all', 'runtime', 'installed'].includes(category) && pluginMarket?.loading)
      "
    >
      <DataList
        :key="category + ':' + (route.query.software || '')"
        :state-key="'market-' + category + ':' + (route.query.software || '')"
        :rows="marketRows"
        row-key="key"
        searchable
        :empty-text="pluginMarket?.loading ? '正在加载插件…' : '此分类暂无应用'"
      >
        <el-table-column label="软件名称" min-width="170">
          <template #default="{ row }">
            <div class="software-row-title">
              <span class="software-logo green">{{ row.symbol }}</span>
              <strong>{{ row.title }}</strong>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="110">
          <template #default="{ row }">
            {{ row.kind === 'plugin' ? (row.plugin.builtin ? '项目维护' : '签名仓库') : '官方源' }}
            <small v-if="row.category === 'docker'" class="version-label">Docker 应用</small>
          </template>
        </el-table-column>
        <el-table-column label="简介" min-width="230">
          <template #default="{ row }">
            <button
              class="software-description"
              :title="row.description"
              @click="introduction = row"
            >
              {{ row.description }}
            </button>
          </template>
        </el-table-column>
        <el-table-column label="版本 / 实例" min-width="210">
          <template #default="{ row }">
            <template v-if="row.kind === 'software'">
              <el-select
                v-model="versions[row.software]"
                :aria-label="row.title + '版本'"
                @change="chosenVersions.add(row.software)"
              >
                <el-option
                  v-for="v in row.versions"
                  :key="v.id"
                  :value="v.id"
                  :label="v.version + (v.disabledReason ? ' · 待验证' : '')"
                />
              </el-select>
            </template>
            <template v-else>
              {{ row.version }}
              <small v-if="row.kind === 'instance'" class="version-label">
                {{ row.resource.name }} ·
                {{ row.resource.config.mode === 'docker' ? 'Docker' : '原生' }}
                <template v-if="row.resource.config.port">:{{ row.resource.config.port }}</template>
              </small>
            </template>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="rowResource(row)?.status === 'ready' ? 'success' : 'info'">
              {{ rowStatus(row) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <div class="market-actions">
              <el-button
                link
                type="primary"
                :disabled="
                  row.kind === 'software'
                    ? !panel
                        .kind('instance')
                        .some(
                          (r) => r.config.software === row.software && r.status !== 'uninstalled',
                        )
                    : !row.resource ||
                      row.resource.status === 'uninstalled' ||
                      row.plugin?.unavailable
                "
                @click="editRow(row)"
              >
                设置
              </el-button>
              <template v-if="row.kind === 'software'">
                <el-tag v-if="selectedInstance(row.software)" size="small" type="info">
                  {{ installationLabel(selectedInstance(row.software)!) }}
                </el-tag>
                <el-button v-else type="primary" size="small" @click="openInstall(row.software)">
                  安装
                </el-button>
              </template>
              <template v-else-if="row.kind === 'plugin'">
                <el-button
                  v-if="!row.resource"
                  type="primary"
                  size="small"
                  @click="pluginMarket?.openPlugin(row.plugin)"
                >
                  安装
                </el-button>
                <el-button
                  v-else-if="
                    row.resource.config.version &&
                    row.resource.config.version !== row.plugin.version &&
                    !row.plugin.unavailable
                  "
                  link
                  type="primary"
                  @click="pluginMarket?.openPlugin(row.plugin)"
                >
                  升级
                </el-button>
                <el-button
                  v-if="
                    row.resource && row.plugin.type !== 'application' && !row.plugin.unavailable
                  "
                  link
                  @click="pluginMarket?.checkPlugin(row.plugin)"
                >
                  检查
                </el-button>
              </template>
              <el-button
                link
                type="danger"
                :disabled="
                  row.kind === 'software'
                    ? !panel.kind('instance').some((r) => r.config.software === row.software)
                    : !row.resource
                "
                @click="removeRow(row)"
              >
                {{ row.resource?.status === 'uninstalled' ? '清理记录' : '卸载' }}
              </el-button>
              <el-dropdown
                v-if="row.kind === 'instance' && row.resource.status !== 'uninstalled'"
                trigger="click"
                @command="(command: string) => instanceAction(row, command)"
              >
                <el-button text :icon="MoreFilled" :aria-label="row.resource.name + '更多操作'" />
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="start">启动</el-dropdown-item>
                    <el-dropdown-item command="stop">停止</el-dropdown-item>
                    <el-dropdown-item command="restart">重启</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </template>
        </el-table-column>
      </DataList>
    </LoadingContent>
  </div>
  <Resources v-if="tab === 'repositories'" kind="repository" />
  <PluginMarket ref="pluginMarket" category="all" headless />
  <SoftwareSettings v-model="settingsOpen" :resource="settingsResource" />
  <AppDialog size="small" v-model="chooseOpen" title="选择实例" width="600px">
    <DataList
      :key="'Market-2' + ':' + tab"
      :state-key="'Market-2' + ':' + tab"
      :rows="matchingInstances"
    >
      <el-table-column prop="name" label="实例" />
      <el-table-column label="版本">
        <template #default="{ row }">{{ row.config.version }}</template>
      </el-table-column>
      <el-table-column label="操作">
        <template #default="{ row }">
          <el-button
            link
            type="primary"
            v-if="row.status !== 'uninstalled'"
            @click="chooseInstance(row)"
          >
            设置
          </el-button>
          <el-button link type="danger" @click="removeInstance(row)">
            {{ row.status === 'uninstalled' ? '清理记录' : '卸载' }}
          </el-button>
        </template>
      </el-table-column>
    </DataList>
  </AppDialog>
  <AppDialog
    size="small"
    :model-value="!!introduction"
    :title="introduction?.title"
    width="650px"
    @close="introduction = undefined"
  >
    <div class="software-intro">
      <span class="software-logo green">{{ introduction?.symbol }}</span>
      <p>{{ introduction?.description }}</p>
      <p v-if="introduction?.kind !== 'plugin'">
        独立版本、配置、日志与数据目录；由 systemd 托管，所有安装和配置变更均可追踪。
      </p>
      <p v-else-if="introduction?.category === 'docker'">
        Docker 应用通过 Compose 部署，端口默认仅绑定本机；安装前会检查引擎和依赖。
      </p>
      <p v-else>插件通过声明式表单配置，安装前会校验来源、权限与兼容范围。</p>
    </div>
  </AppDialog>
  <ResourceForm v-model="install" kind="instance" :initial="initial" />
  <AppDialog
    v-model="importOpen"
    :title="importType === 'catalog' ? '导入签名安装配方' : '导入签名插件清单'"
    width="700px"
  >
    <div class="notice">
      格式包含 keyId、Base64 payload 和 Ed25519 signature。请先通过本机命令信任签名者公钥。
    </div>
    <el-form-item v-if="importType === 'plugin'" label="本地名称">
      <el-input v-model="importName" placeholder="my-plugin" />
    </el-form-item>
    <CodeEditor v-model="importText" />
    <template #footer>
      <el-button @click="importOpen = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="importPackage">验证并导入</el-button>
    </template>
  </AppDialog>
</template>

<style scoped>
.market-toolbar {
  padding: 10px 12px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.market-tools {
  margin-left: auto;
  display: flex;
  gap: 8px;
  align-items: center;
}
.market-categories {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.category-label {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  margin-right: 8px;
}
.market-categories .el-button {
  margin: 0;
  padding: 7px 12px;
}
.software-row-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.software-row-title .software-logo {
  width: 30px;
  height: 30px;
  font-size: 12px;
  flex-shrink: 0;
}
.software-description {
  display: block;
  max-width: 420px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  cursor: pointer;
  color: #475569;
  margin: 0;
  font-size: 13px;
  text-align: left;
  border: 0;
  padding: 0;
  background: transparent;
}
.version-label,
.filter-label {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.version-label {
  display: block;
}
.market-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.market-actions .el-button {
  margin: 0;
}
.software-list {
  padding: 0;
}
.software-intro {
  padding: 12px;
  line-height: 1.8;
}
@media (max-width: 640px) {
  .market-categories .el-button {
    min-height: 44px;
  }
  .market-tools {
    margin-left: 0;
    flex-wrap: wrap;
  }
}
</style>
