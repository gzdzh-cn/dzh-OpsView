<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import { usePageQuery } from '../composables/pageQuery'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
import { api, post, put, action, errorText, type Config } from '../lib/api'
import DataList from './DataList.vue'
import ListPanel from './ListPanel.vue'
import CodeEditor from './CodeEditor.vue'
const props = defineProps<{ category: 'application' | 'operations' | 'all'; headless?: boolean }>()
const panel = usePanel(),
  selected = ref<any>(),
  pluginForm = ref<Config>({}),
  pluginOpen = ref(false),
  busy = ref(false),
  details = ref<any>(),
  importOpen = ref(false),
  importText = ref('')
const rows = computed(() =>
  plugins.value.filter(
    (p) =>
      props.category === 'all' ||
      (props.category === 'application' ? p.type === 'application' : p.type !== 'application'),
  ),
)
const {
  data: plugins,
  error,
  busy: loading,
  hasData,
  load: loadPlugins,
} = usePageQuery<any[]>('/plugins', [], 300000)
const load = () => loadPlugins(true)
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
      installed(p) ? '应用插件设置' : '安装权限',
      { confirmButtonText: installed(p) ? '保存' : '安装', cancelButtonText: '取消' },
    )
    const existing = pluginRecord(p)
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
    await panel.refresh(['resources', 'tasks', 'catalog'])
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
    await panel.refresh(['resources', 'tasks', 'catalog'])
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
    await panel.refresh(['resources', 'tasks', 'catalog'])
    ElMessage.success('插件检查已提交，结果显示在任务中心与资源配置中')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function pluginRecord(p: any) {
  return panel.kind('plugin').find((r) => r.config.pluginId === p.id || r.name === p.id)
}
function installed(p: any) {
  const record = pluginRecord(p)
  return record?.status === 'uninstalled' ? undefined : record
}

async function importPackage() {
  try {
    const resource = await post('/plugins/import', { envelope: JSON.parse(importText.value) })
    await action(resource.id, 'install')
    importOpen.value = false
    await panel.refresh(['resources', 'tasks', 'catalog'])
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
defineExpose({
  plugins,
  loading,
  hasData,
  error,
  load,
  installed,
  openPlugin,
  removePlugin,
  checkPlugin,
  openImport: () => (importOpen.value = true),
})
</script>
<template>
  <ListPanel
    v-if="!headless"
    :count="rows.length"
    :busy="loading"
    :ready="hasData"
    :error="error"
    @retry="load"
  >
    <template #actions>
      <el-button @click="load">刷新</el-button>
      <el-button @click="importOpen = true">导入签名插件</el-button>
    </template>
    <DataList state-key="PluginMarket-1" :rows="rows" searchable>
      <el-table-column prop="name" label="名称" width="170" />
      <el-table-column label="简介" min-width="250">
        <template #default="{ row }">
          <el-button link class="list-ellipsis" :title="row.description" @click="details = row">
            {{ row.description }}
          </el-button>
        </template>
      </el-table-column>
      <el-table-column label="来源 / 版本" width="170">
        <template #default="{ row }">
          {{ row.builtin ? '项目维护' : '已签名仓库' }}
          <small class="cell-subtext">{{ row.version }}</small>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">{{ installed(row)?.status || '未安装' }}</template>
      </el-table-column>
      <el-table-column label="操作" fixed="right" width="230">
        <template #default="{ row }">
          <el-button link type="primary" :disabled="!installed(row)" @click="openPlugin(row)">
            设置
          </el-button>
          <el-button v-if="!installed(row)" type="primary" size="small" @click="openPlugin(row)">
            安装
          </el-button>
          <el-button
            v-else-if="
              installed(row)?.config.version && installed(row)?.config.version !== row.version
            "
            link
            type="primary"
            @click="openPlugin(row)"
          >
            升级
          </el-button>
          <el-button
            v-if="installed(row) && row.type !== 'application'"
            link
            @click="checkPlugin(row)"
          >
            检查
          </el-button>
          <el-button link type="danger" :disabled="!installed(row)" @click="removePlugin(row)">
            卸载
          </el-button>
        </template>
      </el-table-column>
    </DataList>
  </ListPanel>
  <AppDialog
    size="small"
    :model-value="!!details"
    :title="details?.name"
    @close="details = undefined"
  >
    <p>{{ details?.description }}</p>
    <p>权限：{{ details?.permissions?.join('、') || '无额外权限' }}</p>
    <p>依赖：{{ details?.dependencies?.join('、') || '无' }}</p>
  </AppDialog>
  <AppDialog
    v-model="pluginOpen"
    :title="(selected && installed(selected) ? '设置 ' : '安装 ') + (selected?.name || '')"
    width="540px"
  >
    <p>{{ selected?.description }}</p>
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
          :show-password="f.type === 'password'"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="pluginOpen = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="installPlugin">
        {{ selected && installed(selected) ? '保存设置' : '安装' }}
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="importOpen" title="导入签名插件" width="700px">
    <p>名称从签名清单自动读取，安装前由执行器验证受信任签名与权限。</p>
    <CodeEditor v-model="importText" />
    <template #footer>
      <el-button @click="importOpen = false">取消</el-button>
      <el-button type="primary" @click="importPackage">验证并导入</el-button>
    </template>
  </AppDialog>
</template>
