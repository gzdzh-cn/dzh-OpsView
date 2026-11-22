<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import { usePageQuery } from '../composables/pageQuery'
import LoadingContent from '../components/LoadingContent.vue'
import DataList from '../components/DataList.vue'
import { ref, nextTick } from 'vue'
import { post, errorText } from '../lib/api'
import { ElMessage } from 'element-plus'
import FirewallManagement from '../components/FirewallManagement.vue'
import SSHManagement from '../components/SSHManagement.vue'
const tab = usePageState('tab', 'firewall')
const firewall = ref<InstanceType<typeof FirewallManagement>>()
async function openSSHProtection() {
  tab.value = 'firewall'
  await nextTick()
  await firewall.value?.openProtection()
}
const {
  data,
  hasData,
  error,
  busy,
  load: loadSystem,
} = usePageQuery<any>(() => (tab.value === 'services' ? '/system' : ''), null)
const {
  data: audits,
  hasData: auditReady,
  busy: auditBusy,
  error: auditError,
  load: loadAudit,
} = usePageQuery<any[]>(() => (tab.value === 'audit' ? '/audit' : ''), [])
const unit = ref(''),
  logs = ref('')
const load = () => loadSystem(true)
const audit = () => loadAudit(true)
async function readLogs() {
  try {
    logs.value = (await post('/system/actions/logs', { unit: unit.value })).content
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="系统防火墙" name="firewall" />
    <el-tab-pane label="SSH 管理" name="ssh" />
    <el-tab-pane label="端口与服务" name="services" />
    <el-tab-pane label="系统日志" name="logs" />
    <el-tab-pane label="操作审计" name="audit" />
  </el-tabs>
  <FirewallManagement v-if="tab === 'firewall'" ref="firewall" />
  <SSHManagement v-if="tab === 'ssh'" @protection="openSSHProtection" />
  <template v-if="tab === 'services'">
    <el-alert v-if="error" :title="error" type="error" :closable="false">
      <el-button link @click="load">重试</el-button>
    </el-alert>
    <LoadingContent :busy="busy" :ready="hasData">
      <div class="notice">
        监听端口与防火墙放行是不同状态。云厂商安全组需单独检查；已有服务只检测，不自动接管。
      </div>
      <section class="surface" style="margin-bottom: var(--ops-space)">
        <div class="page-toolbar">
          <h3>监听端口</h3>
          <el-button :loading="busy" @click="load">刷新</el-button>
        </div>
        <DataList
          :key="'System-1' + ':' + tab"
          :state-key="'System-1' + ':' + tab"
          :rows="data?.portItems || []"
          searchable
        >
          <el-table-column prop="protocol" label="协议" width="90" />
          <el-table-column prop="local" label="监听地址" min-width="200" />
          <el-table-column prop="state" label="状态" width="110" />
          <el-table-column prop="process" label="进程" min-width="240" show-overflow-tooltip />
        </DataList>
        <el-collapse>
          <el-collapse-item title="原始端口诊断">
            <pre class="log-box">{{ data?.ports }}</pre>
          </el-collapse-item>
        </el-collapse>
      </section>
      <section class="surface">
        <div class="page-toolbar"><h3>系统服务</h3></div>
        <DataList
          :key="'System-2' + ':' + tab"
          :state-key="'System-2' + ':' + tab"
          :rows="data?.serviceItems || []"
          searchable
        >
          <el-table-column prop="unit" label="服务" min-width="220" />
          <el-table-column prop="active" label="状态" width="110" />
          <el-table-column prop="state" label="运行状态" width="120" />
          <el-table-column prop="description" label="说明" min-width="250" show-overflow-tooltip />
          <el-table-column label="归属" width="110">
            <template #default="{ row }">{{ row.managed ? 'OpsView' : '外部服务' }}</template>
          </el-table-column>
        </DataList>
        <el-collapse>
          <el-collapse-item title="原始服务诊断">
            <pre class="log-box">{{ data?.services }}</pre>
          </el-collapse-item>
        </el-collapse>
      </section>
    </LoadingContent>
  </template>
  <section v-if="tab === 'logs'" class="surface system-detail">
    <div class="page-toolbar">
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
    <el-alert v-if="auditError" :title="auditError" type="error" :closable="false">
      <el-button link @click="audit">重试</el-button>
    </el-alert>
    <LoadingContent :busy="auditBusy" :ready="auditReady">
      <DataList :key="'System-3' + ':' + tab" :state-key="'System-3' + ':' + tab" :rows="audits">
        <el-table-column label="操作" prop="action" min-width="180" />
        <el-table-column label="资源" prop="resourceId" min-width="180" />
        <el-table-column label="来源 IP" prop="ip" width="140" />
        <el-table-column label="时间" prop="createdAt" min-width="170" />
      </DataList>
    </LoadingContent>
  </section>
</template>
