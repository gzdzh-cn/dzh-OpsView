<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Box, Grid, Upload } from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { api, post, put, action, errorText, type Config } from '../lib/api'
import ResourceForm from '../components/ResourceForm.vue'
import Resources from './Resources.vue'
import SoftwareSettings from '../components/SoftwareSettings.vue'
import type { Resource } from '../lib/api'
import CodeEditor from '../components/CodeEditor.vue'
const panel = usePanel(),
  tab = ref('software'),
  install = ref(false),
  initial = ref<Config>({}),
  plugins = ref<any[]>([]),
  importOpen = ref(false),
  importType = ref('plugin'),
  importName = ref(''),
  importText = ref(''),
  busy = ref(false),
  selected = ref<any>(),
  pluginForm = ref<Config>({}),
  pluginOpen = ref(false)
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
const versions = ref<Record<string, string>>({})
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
  const items = matchingInstances.value
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
      `卸载 ${r.name}，保留业务数据。有关联资源时将拒绝卸载。`,
      '卸载实例',
      { confirmButtonText: '卸载', cancelButtonText: '取消' },
    )
    await action(r.id, 'uninstall')
    chooseOpen.value = false
    await panel.refresh()
    ElMessage.success('卸载任务已提交')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
function openImport(type: string) {
  importType.value = type
  importOpen.value = true
}
async function loadPlugins() {
  try {
    plugins.value = await api('/plugins')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
onMounted(loadPlugins)
watch(
  groups,
  (items) => {
    for (const g of items) {
      if (!g.versions.some((v) => v.id === versions.value[g.software]))
        versions.value[g.software] =
          (
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
function openPlugin(p: any) {
  selected.value = p
  pluginForm.value = { ...(installed(p)?.config.values || {}) }
  for (const f of p.form || [])
    if (pluginForm.value[f.key] === undefined) pluginForm.value[f.key] = f.default
  pluginOpen.value = true
}
async function installPlugin() {
  if (!selected.value) return
  busy.value = true
  try {
    const p = selected.value
    await ElMessageBox.confirm(
      `插件来源：${p.builtin ? '项目内置' : '已签名仓库'}\n请求权限：${(p.permissions || []).join('、') || '无额外权限'}。应用插件的端口默认仅绑定本机。`,
      '安装权限',
      { confirmButtonText: '安装', cancelButtonText: '取消' },
    )
    const existing = installed(p)
    const config = {
      pluginId: p.id,
      builtin: !!p.builtin,
      envelope: p.envelope,
      values: pluginForm.value,
    }
    const resource = existing
      ? await put('/resources/' + existing.id, {
          name: existing.name,
          config,
          revision: existing.revision,
        })
      : await post('/resources', { kind: 'plugin', name: p.id, config })
    await action(resource.id, 'install')
    pluginOpen.value = false
    await panel.refresh()
    ElMessage.success('已提交插件安装任务')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
async function removePlugin(p: any) {
  const resource = panel.kind('plugin').find((r) => r.config.pluginId === p.id || r.name === p.id)
  if (!resource) return
  try {
    await ElMessageBox.confirm('卸载后保留配置、业务数据和卷。', '卸载插件', {
      confirmButtonText: '卸载',
      cancelButtonText: '取消',
    })
    if (resource.status === 'pending' || resource.status === 'error') {
      try {
        await action(resource.id, 'uninstall')
      } catch {
        await api('/resources/' + resource.id, { method: 'DELETE' })
      }
    } else await action(resource.id, 'uninstall')
    await panel.refresh()
    ElMessage.success('已提交卸载任务')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function checkPlugin(p: any) {
  const resource = installed(p)
  if (!resource) return
  try {
    await action(resource.id, 'check')
    await panel.refresh()
    ElMessage.success('插件检查已提交，结果显示在任务中心与资源配置中')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function installed(p: any) {
  return panel.kind('plugin').find((r) => r.config.pluginId === p.id || r.name === p.id)
}
async function importPackage() {
  busy.value = true
  try {
    const envelope = JSON.parse(importText.value)
    if (importType.value === 'catalog') {
      await post('/catalog/import', envelope)
      await panel.refresh()
    } else {
      const resource = await post('/plugins/import', { name: importName.value, envelope })
      await action(resource.id, 'install')
      await panel.refresh()
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
    <el-tab-pane label="核心软件" name="software" />
    <el-tab-pane label="插件市场" name="plugins" />
    <el-tab-pane label="仓库管理" name="repositories" />
  </el-tabs>
  <template v-if="tab === 'software'">
    <div class="notice">
      原生安装支持固定版本及独立实例。预编译包与源码配方按实际验证状态提供；安装过程可在任务中心查看。
    </div>
    <div class="surface software-list">
      <el-table :data="groups" row-key="software">
        <el-table-column label="软件" width="150">
          <template #default="{ row: g }">
            <div class="software-row-title">
              <span class="software-logo green">{{ g.symbol }}</span>
              <strong>{{ g.title }}</strong>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="简介" min-width="250">
          <template #default="{ row: g }">
            <p class="software-description" @click="introduction = g">{{ g.description }}</p>
            <small>项目维护 · 原生安装</small>
          </template>
        </el-table-column>
        <el-table-column label="已安装版本" min-width="180">
          <template #default="{ row: g }">
            <span
              v-for="r in panel.kind('instance').filter((r) => r.config.software === g.software)"
              :key="r.id"
              class="version-label"
            >
              {{ r.config.version }} · {{ r.status }}
            </span>
            <span v-if="!panel.kind('instance').some((r) => r.config.software === g.software)">
              未安装
            </span>
          </template>
        </el-table-column>
        <el-table-column label="可用版本" width="175">
          <template #default="{ row: g }">
            <el-select v-model="versions[g.software]">
              <el-option
                v-for="v in g.versions"
                :key="v.id"
                :value="v.id"
                :label="v.version + (v.disabledReason ? ' · 待验证' : '')"
              />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row: g }">
            <el-button
              link
              type="primary"
              :disabled="!panel.kind('instance').some((r) => r.config.software === g.software)"
              @click="openSettings(g.software)"
            >
              设置
            </el-button>
            <el-button link type="primary" @click="openInstall(g.software)">安装</el-button>
            <el-button
              link
              type="danger"
              :disabled="!panel.kind('instance').some((r) => r.config.software === g.software)"
              @click="uninstallSoftware(g.software)"
            >
              卸载
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>
    <div class="split-heading">
      <h3>已配置实例</h3>
      <el-button text :icon="Upload" @click="openImport('catalog')">导入安装配方</el-button>
    </div>
    <Resources kind="instance" embedded />
  </template>
  <template v-if="tab === 'plugins'">
    <div class="split-heading" style="margin-top: 0">
      <div>
        <h3>扩展你的工作空间</h3>
        <p class="form-help" style="margin-bottom: 0">应用和运维能力，使用独立插件规范。</p>
      </div>
      <el-button :icon="Upload" @click="openImport('plugin')">导入插件</el-button>
    </div>
    <div class="market-grid">
      <article v-for="p in plugins" :key="p.id" class="surface software-card">
        <div class="software-top">
          <span
            class="software-logo"
            :class="p.type === 'backup' ? 'amber' : p.id === 'redis' ? 'purple' : 'blue'"
          >
            <el-icon>
              <Grid v-if="p.type === 'backup'" />
              <Box v-else />
            </el-icon>
          </span>
          <div>
            <h3>{{ p.name }}</h3>
            <small>v{{ p.version }} · {{ p.builtin ? '内置维护' : '已签名仓库' }}</small>
          </div>
        </div>
        <p>{{ p.description }}</p>
        <div class="software-pills">
          <span v-for="permission in p.permissions" :key="permission">{{ permission }}</span>
        </div>
        <div class="software-bottom">
          <span>{{ p.type === 'application' ? '应用插件' : '运维扩展' }}</span>
          <el-button v-if="installed(p)" text @click="openPlugin(p)">配置 / 升级</el-button>
          <el-button
            v-if="installed(p)?.status === 'ready' && p.type !== 'application'"
            text
            @click="checkPlugin(p)"
          >
            检查
          </el-button>
          <el-button v-if="installed(p)" plain @click="removePlugin(p)">
            {{ installed(p)?.status === 'ready' ? '卸载' : '移除 / 重试' }}
          </el-button>
          <el-button v-else type="primary" plain @click="openPlugin(p)">安装插件</el-button>
        </div>
      </article>
    </div>
    <div v-if="!plugins.length" class="surface empty-page">
      <h3>暂无插件</h3>
      <p>添加自建仓库或导入签名插件清单。</p>
    </div>
  </template>
  <Resources v-if="tab === 'repositories'" kind="repository" />
  <SoftwareSettings v-model="settingsOpen" :resource="settingsResource" />
  <el-dialog v-model="chooseOpen" title="选择实例" width="600px">
    <el-table :data="matchingInstances">
      <el-table-column prop="name" label="实例" />
      <el-table-column label="版本">
        <template #default="{ row }">{{ row.config.version }}</template>
      </el-table-column>
      <el-table-column label="操作">
        <template #default="{ row }">
          <el-button link type="primary" @click="chooseInstance(row)">设置</el-button>
          <el-button link type="danger" @click="removeInstance(row)">卸载</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-dialog>
  <el-dialog
    :model-value="!!introduction"
    :title="introduction?.title"
    width="650px"
    @close="introduction = undefined"
  >
    <div class="software-intro">
      <span class="software-logo green">{{ introduction?.symbol }}</span>
      <h2>{{ introduction?.title }}</h2>
      <p>{{ introduction?.description }}</p>
      <p>独立版本、配置、日志与数据目录；由 systemd 托管，所有安装和配置变更均可追踪。</p>
    </div>
  </el-dialog>
  <ResourceForm v-model="install" kind="instance" :initial="initial" />
  <el-dialog v-model="pluginOpen" :title="'安装 ' + (selected?.name || '')" width="520px">
    <p class="form-help">{{ selected?.description }}</p>
    <el-form label-position="top">
      <el-form-item v-for="f in selected?.form || []" :key="f.key" :label="f.label">
        <el-input-number
          v-if="f.type === 'port'"
          v-model="pluginForm[f.key]"
          :min="1"
          :max="65535"
        />
        <el-input
          v-else
          v-model="pluginForm[f.key]"
          :type="f.type === 'password' ? 'password' : 'text'"
          show-password
        />
      </el-form-item>
    </el-form>
    <div class="notice">插件安装前将显示权限请求。第三方清单必须通过受信任公钥验证。</div>
    <template #footer>
      <el-button @click="pluginOpen = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="installPlugin">继续安装</el-button>
    </template>
  </el-dialog>
  <el-dialog
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
  </el-dialog>
</template>

<style scoped>
.software-row-title {
  display: flex;
  align-items: center;
  gap: 12px;
}
.software-row-title .software-logo {
  width: 38px;
  height: 38px;
  font-size: 15px;
}
.software-description {
  max-width: 420px;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  cursor: pointer;
  color: #475569;
  margin: 5px 0;
}
.software-list small {
  color: #94a3b8;
}
.version-label {
  display: block;
  font-size: 12px;
}
.software-intro {
  padding: 24px;
  line-height: 1.8;
}
.software-list {
  padding: 8px 16px;
}
</style>
