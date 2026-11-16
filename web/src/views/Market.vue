<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Box, Grid, Upload, ArrowRight } from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { api, post, put, action, errorText, type Config } from '../lib/api'
import ResourceForm from '../components/ResourceForm.vue'
import Resources from './Resources.vue'
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
  ['nginx', 'mysql', 'postgresql', 'nodejs'].map((software) => ({
    software,
    versions: panel.catalog.filter((v) => v.software === software),
    title:
      software === 'nginx'
        ? 'Nginx'
        : software === 'mysql'
          ? 'MySQL'
          : software === 'nodejs'
            ? 'Node.js'
            : 'PostgreSQL',
    symbol:
      software === 'nginx' ? 'N' : software === 'mysql' ? 'M' : software === 'nodejs' ? 'JS' : 'P',
    description:
      software === 'nginx'
        ? '高性能 Web 服务与反向代理，连接域名、静态站点和你的应用。'
        : software === 'mysql'
          ? '为业务应用提供可靠的关系型数据存储，独立版本与实例管理。'
          : software === 'nodejs'
            ? '安装独立的 Node.js LTS 运行时，为应用选择准确版本的可执行文件。'
            : '功能丰富的开源关系型数据库，为复杂查询与数据应用提供支持。',
  })),
)
const versions = ref<Record<string, string>>({})
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
onMounted(async () => {
  await loadPlugins()
  for (const g of groups.value) versions.value[g.software] = g.versions[0]?.id || ''
})
function openInstall(software: string) {
  const entry = panel.catalog.find((v) => v.id === versions.value[software])
  initial.value = {
    catalogId: entry?.id,
    mode: entry?.source ? 'native' : 'docker',
    port:
      software === 'mysql'
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
      按需选择原生或 Docker 安装。所有安装源包含准确版本及校验值；自定义安装源通过签名配方导入。
    </div>
    <div class="market-grid">
      <article v-for="g in groups" :key="g.software" class="surface software-card">
        <div class="software-top">
          <span
            class="software-logo"
            :class="
              g.software === 'mysql' ? 'blue' : g.software === 'postgresql' ? 'purple' : 'green'
            "
          >
            {{ g.symbol }}
          </span>
          <div>
            <h3>{{ g.title }}</h3>
            <small>CORE SOFTWARE</small>
          </div>
        </div>
        <p>{{ g.description }}</p>
        <div class="software-pills">
          <span>原生安装</span>
          <span v-if="g.versions.some((v) => v.image)">Docker</span>
          <span>独立实例</span>
        </div>
        <el-select v-model="versions[g.software]" placeholder="选择版本">
          <el-option
            v-for="v in g.versions"
            :key="v.id"
            :value="v.id"
            :label="`${v.version} · ${v.source && v.image ? '原生 / Docker' : v.source ? '原生' : 'Docker'}`"
          />
        </el-select>
        <div class="software-bottom">
          <span>
            {{
              panel.kind('instance').filter((r) => r.config.software === g.software).length
            }}
            个实例
          </span>
          <el-button type="primary" plain @click="openInstall(g.software)">
            安装
            <el-icon><ArrowRight /></el-icon>
          </el-button>
        </div>
      </article>
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
