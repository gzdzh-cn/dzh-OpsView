<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, post, errorText } from '../lib/api'
import { ElMessage } from 'element-plus'
import Resources from './Resources.vue'
const tab = ref('firewall'),
  data = ref<any>(),
  error = ref(''),
  audits = ref<any[]>([]),
  unit = ref(''),
  logs = ref(''),
  busy = ref(false)
async function load() {
  busy.value = true
  error.value = ''
  try {
    data.value = await api('/system')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function audit() {
  try {
    audits.value = await api('/audit')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function readLogs() {
  try {
    logs.value = (await post('/system/actions/logs', { unit: unit.value })).content
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
onMounted(() => {
  void load()
  void audit()
})
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="防火墙规则" name="firewall" />
    <el-tab-pane label="端口与服务" name="services" />
    <el-tab-pane label="系统日志" name="logs" />
    <el-tab-pane label="操作审计" name="audit" />
  </el-tabs>
  <template v-if="tab === 'firewall'">
    <div class="notice warn">
      宿主机与 Docker 入站规则分别管理。应用后须在 120
      秒内确认，否则自动回退；已有不兼容的防火墙体系只提供诊断。
    </div>
    <Resources kind="firewall" />
    <div class="split-heading">
      <h3>当前实际规则</h3>
      <el-button :loading="busy" @click="load">重新检查</el-button>
    </div>
    <div v-if="error" class="notice warn">{{ error }}</div>
    <div class="system-grid">
      <section class="surface system-detail">
        <h3>宿主机 · UFW</h3>
        <pre class="log-box">{{ data?.firewall || '等待执行器返回状态' }}</pre>
      </section>
      <section class="surface system-detail">
        <h3>Docker · DOCKER-USER</h3>
        <pre class="log-box">{{ data?.dockerRules || '等待执行器返回状态' }}</pre>
      </section>
    </div>
  </template>
  <template v-if="tab === 'services'">
    <div v-if="error" class="notice warn">{{ error }}</div>
    <div class="notice">
      监听端口与防火墙放行是不同状态。云厂商安全组需单独检查；已有服务只检测，不自动接管。
    </div>
    <section class="surface system-detail" style="margin-bottom: 20px">
      <h3>监听端口</h3>
      <pre class="log-box">{{ data?.ports || '执行器尚未返回状态' }}</pre>
    </section>
    <section class="surface system-detail">
      <h3>系统服务</h3>
      <pre class="log-box">{{ data?.services || '执行器尚未返回状态' }}</pre>
    </section>
  </template>
  <section v-if="tab === 'logs'" class="surface system-detail">
    <h3>服务日志</h3>
    <div class="page-toolbar-left" style="margin-bottom: 18px">
      <el-input v-model="unit" placeholder="opsview-panel.service" style="max-width: 400px" />
      <el-button type="primary" @click="readLogs">读取最近 200 行</el-button>
    </div>
    <pre class="log-box">{{ logs || '填写 systemd 服务名称后读取日志。' }}</pre>
  </section>
  <section v-if="tab === 'audit'" class="surface">
    <div class="page-toolbar">
      <h3>操作审计</h3>
      <el-button @click="audit">刷新</el-button>
    </div>
    <el-table :data="audits">
      <el-table-column label="操作" prop="action" min-width="180" />
      <el-table-column label="资源" prop="resourceId" min-width="180" />
      <el-table-column label="来源 IP" prop="ip" width="140" />
      <el-table-column label="时间" prop="createdAt" min-width="170" />
    </el-table>
  </section>
</template>
