<script setup lang="ts">
import PluginMarket from '../components/PluginMarket.vue'
import DataList from '../components/DataList.vue'
import { ref, watch, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Box, RefreshRight, Plus } from '@element-plus/icons-vue'
import { api, post, errorText, size } from '../lib/api'
import { usePanel } from '../stores/panel'
import Resources from './Resources.vue'
const engine = ref<any>(),
  engineBusy = ref(false)
async function inspect() {
  engineBusy.value = true
  error.value = ''
  try {
    engine.value = await api('/docker/status')
    if (engine.value.status === 'ready') await load()
  } catch (e) {
    error.value = errorText(e)
    engine.value = { status: 'error', reason: error.value }
    items.value = []
  } finally {
    engineBusy.value = false
  }
}
async function startEngine() {
  try {
    await post('/system/actions/start-docker')
    ElMessage.success('启动任务已提交，请稍后重新检测')
    await panel.refresh()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
const tab = ref('containers'),
  items = ref<any[]>([]),
  busy = ref(false),
  error = ref(''),
  content = ref(''),
  showLog = ref(false),
  logTitle = ref('容器日志'),
  panel = usePanel()
async function load() {
  if (['compose', 'templates'].includes(tab.value) || engine.value?.status !== 'ready') return
  busy.value = true
  error.value = ''
  try {
    const data = await api('/docker/' + tab.value)
    items.value = Array.isArray(data.items) ? data.items : data.items?.Volumes || []
  } catch (e) {
    error.value = errorText(e)
    items.value = []
  } finally {
    busy.value = false
  }
}
async function run(id: string, action: string) {
  try {
    if (!['logs', 'stats'].includes(action))
      await ElMessageBox.confirm(
        action === 'remove'
          ? '删除已停止的容器，保留数据卷。受面板管理的应用请使用对应实例或 Compose 页面卸载。确认删除？'
          : `确认${action === 'stop' ? '停止' : action === 'start' ? '启动' : '重启'}此容器？`,
        '容器操作',
        { confirmButtonText: '确认', cancelButtonText: '取消' },
      )
    const data = await post(`/docker/containers/${id}/${action}`, {
      confirmContainerDeletion: action === 'remove',
    })
    if (['logs', 'stats'].includes(action)) {
      content.value = action === 'logs' ? data.content || '暂无日志' : JSON.stringify(data, null, 2)
      logTitle.value = action === 'logs' ? '容器日志' : '容器资源快照'
      showLog.value = true
    } else {
      ElMessage.success('任务已提交')
      await panel.refresh()
    }
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function manage(action: string, row?: any) {
  try {
    let name = row?.Name || row?.Id || row?.ID || '',
      confirmation = ''
    if (action === 'delete') {
      const answer = await ElMessageBox.prompt(
        `请输入 ${name} 确认删除。删除数据卷会永久删除其中的数据；正在使用的资源由 Docker 拒绝删除。`,
        '删除 Docker 资源',
        {
          confirmButtonText: '删除',
          cancelButtonText: '取消',
          inputValidator: (v) => v === name || '输入必须与名称完全一致',
        },
      )
      confirmation = answer.value
    } else {
      const answer = await ElMessageBox.prompt(
        tab.value === 'images'
          ? '输入完整镜像引用，必须包含 @sha256: 摘要'
          : '输入资源名称，创建时自动添加 opsview- 前缀',
        tab.value === 'images' ? '拉取固定镜像' : '创建资源',
        { confirmButtonText: '提交', cancelButtonText: '取消' },
      )
      name = answer.value
    }
    await post(`/docker/manage/${tab.value}/${action}`, { name, confirmation })
    ElMessage.success('任务已提交，请在任务完成后刷新列表')
    await panel.refresh()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
function canDelete(row: any) {
  return (
    tab.value === 'images' ||
    row.Labels?.['opsview.managed'] === 'true' ||
    row.Labels?.['com.docker.compose.project']?.startsWith('opsview-')
  )
}
async function install() {
  try {
    await ElMessageBox.confirm(
      '将从 Docker 官方软件源安装 Docker Engine 与 Compose，适用于未安装引擎的 Ubuntu/Debian。',
      '安装 Docker',
      { confirmButtonText: '安装', cancelButtonText: '取消' },
    )
    await post('/system/actions/install-docker')
    await panel.refresh()
    ElMessage.success('安装任务已提交')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
watch(tab, load)
onMounted(inspect)
</script>
<template>
  <div class="notice" v-if="engine?.status === 'ready'">
    Docker {{ engine.engineVersion }} · API {{ engine.apiVersion }} · Compose
    {{ engine.composeAvailable ? engine.composeVersion : '未安装' }}
    <el-button link @click="inspect">重新检测</el-button>
  </div>
  <section v-if="!engine || engine.status !== 'ready'" class="surface empty-page">
    <h3>{{ engine?.reason || '正在检测 Docker 引擎…' }}</h3>
    <p v-if="engine?.status === 'incompatible'">面板支持 API 1.41–1.47，请手动配置兼容引擎。</p>
    <el-button :loading="engineBusy" @click="inspect">重新检测</el-button>
    <el-button v-if="engine?.status === 'not_installed'" type="primary" @click="install">
      安装 Docker 引擎
    </el-button>
    <el-button v-if="engine?.status === 'stopped'" type="primary" @click="startEngine">
      启动 Docker 服务
    </el-button>
  </section>
  <el-tabs v-if="engine?.status === 'ready'" v-model="tab" class="panel-tabs">
    <el-tab-pane label="容器" name="containers" />
    <el-tab-pane label="镜像" name="images" />
    <el-tab-pane label="网络" name="networks" />
    <el-tab-pane label="数据卷" name="volumes" />
    <el-tab-pane label="Compose 项目" name="compose" />
    <el-tab-pane label="应用模板" name="templates" />
  </el-tabs>
  <PluginMarket v-if="engine?.status === 'ready' && tab === 'templates'" category="application" />
  <Resources v-if="engine?.status === 'ready' && tab === 'compose'" kind="compose" />
  <section
    v-if="engine?.status === 'ready' && !['compose', 'templates'].includes(tab)"
    class="surface"
  >
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <h3>
          {{ { containers: '容器', images: '镜像', networks: '网络', volumes: '数据卷' }[tab] }}
        </h3>
        <span class="count-badge">{{ items.length }}</span>
      </div>
      <div class="page-toolbar-right">
        <el-button :icon="RefreshRight" :loading="busy" @click="load">刷新</el-button>
        <el-button
          v-if="tab !== 'containers'"
          :icon="Plus"
          @click="manage(tab === 'images' ? 'pull' : 'create')"
        >
          {{ tab === 'images' ? '拉取镜像' : '创建' }}
        </el-button>
      </div>
    </div>
    <div v-if="error" class="notice warn" style="margin: 0 20px 20px">{{ error }}</div>
    <DataList v-if="items.length" :rows="items">
      <el-table-column label="名称 / ID" min-width="200">
        <template #default="{ row }">
          <span class="task-operation">
            {{ (row.Names?.[0] || row.RepoTags?.[0] || row.Name || row.Id)?.replace(/^\//, '') }}
          </span>
          <small class="cell-subtext">{{ (row.Id || row.ID || row.Name)?.slice(0, 20) }}</small>
        </template>
      </el-table-column>
      <el-table-column v-if="tab === 'containers'" label="镜像 / 状态" min-width="200">
        <template #default="{ row }">
          <span>{{ row.Image }}</span>
          <small class="cell-subtext">{{ row.Status }}</small>
        </template>
      </el-table-column>
      <el-table-column v-if="tab === 'containers'" label="发布端口" min-width="140">
        <template #default="{ row }">
          <span v-if="row.HostConfig?.NetworkMode === 'host'">宿主机网络 · 见监听端口</span>
          <small
            v-for="(p, i) in row.Ports?.filter((p: any) => p.PublicPort)"
            :key="i"
            class="cell-subtext"
          >
            {{ p.IP }}:{{ p.PublicPort }} → {{ p.PrivatePort }}/{{ p.Type }}
          </small>
        </template>
      </el-table-column>
      <el-table-column v-if="tab === 'images'" label="大小">
        <template #default="{ row }">{{ size(row.Size) }}</template>
      </el-table-column>
      <el-table-column v-if="['networks', 'volumes'].includes(tab)" label="驱动" prop="Driver" />
      <el-table-column label="操作" width="220">
        <template #default="{ row }">
          <div v-if="tab === 'containers'" class="row-actions">
            <button @click="run(row.Id, row.State === 'running' ? 'restart' : 'start')">
              {{ row.State === 'running' ? '重启' : '启动' }}
            </button>
            <button v-if="row.State === 'running'" @click="run(row.Id, 'stop')">停止</button>
            <button @click="run(row.Id, 'logs')">日志</button>
            <button @click="run(row.Id, 'stats')">资源</button>
            <button
              v-if="
                row.State !== 'running' &&
                !row.Labels?.['opsview.managed'] &&
                !row.Labels?.['com.docker.compose.project']?.startsWith('opsview-')
              "
              @click="run(row.Id, 'remove')"
            >
              删除
            </button>
          </div>
          <div v-else class="row-actions">
            <button v-if="canDelete(row)" @click="manage('delete', row)">删除</button>
            <span v-else class="form-help">外部资源</span>
          </div>
        </template>
      </el-table-column>
    </DataList>
    <div v-else class="empty-page">
      <span class="empty-symbol"><Box /></span>
      <h3>{{ error ? 'Docker 暂不可用' : busy ? '正在读取…' : '当前没有资源' }}</h3>
      <p>
        连接 Linux 执行器和 Docker 引擎后显示真实资源。应用可以通过 Compose 项目或插件市场部署。
      </p>
    </div>
  </section>
  <el-dialog v-model="showLog" :title="logTitle" width="850px">
    <pre class="log-box">{{ content }}</pre>
  </el-dialog>
</template>
