<script setup lang="ts">
import { ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import DataList from './DataList.vue'
import Resources from '../views/Resources.vue'
import { usePageState } from '../composables/pageState'
import { usePageQuery } from '../composables/pageQuery'
import { useSecurityActions } from '../composables/securityActions'
import type { Config } from '../lib/api'
const { data, busy: reading, error, load } = usePageQuery<Config>('/system/firewall', {})
const { busy, taskId, run } = useSecurityActions(() => load(true))
const view = usePageState('firewall-view', 'managed')
const modeOpen = ref(false),
  source = ref(''),
  acknowledged = ref(false)
function openMode() {
  acknowledged.value = false
  modeOpen.value = true
}
async function changeMode() {
  if (
    await run('firewall-mode', {
      enabled: !data.value.enabled,
      source: source.value,
      confirmedImpact: acknowledged.value,
    })
  )
    modeOpen.value = false
}
async function confirm() {
  try {
    await ElMessageBox.confirm(
      '请先重新打开面板并测试 SSH 新连接，确认当前防火墙规则没有阻断访问。',
      '保留防火墙状态',
      { confirmButtonText: '访问正常，保留', cancelButtonText: '取消' },
    )
    await run('firewall-confirm', { connected: true }, false)
  } catch {
    /* Cancel keeps the automatic rollback. */
  }
}
</script>
<template>
  <section class="surface security-panel" v-loading="reading">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-tag :type="data.enabled ? 'success' : 'warning'">
          UFW {{ data.installed ? (data.enabled ? '已启用' : '未启用') : '不可用' }}
        </el-tag>
        <el-button
          :disabled="!data.compatible || busy || Boolean(data.pending?.deadline)"
          @click="openMode"
        >
          {{ data.enabled ? '关闭防火墙' : '启用防火墙' }}
        </el-button>
        <el-button
          :disabled="!data.compatible || !data.enabled || busy"
          @click="run('firewall-reload')"
        >
          重载规则
        </el-button>
      </div>
      <el-button :loading="reading" @click="load(true)">检查规则</el-button>
    </div>
    <el-alert
      v-if="error || data.reason"
      :title="error || data.reason"
      type="warning"
      :closable="false"
    />
    <el-alert
      v-if="data.pending?.deadline"
      type="warning"
      :closable="false"
      class="security-pending"
    >
      <template #title>
        状态待确认 · {{ new Date(data.pending.deadline * 1000).toLocaleTimeString() }} 超时回退
      </template>
      <p v-if="data.pending.rollbackError">回退待重试：{{ data.pending.rollbackError }}</p>
      <el-button type="primary" :disabled="busy" @click="confirm">访问正常，确认保留</el-button>
      <el-button :disabled="busy" @click="run('firewall-rollback', {}, false)">立即回退</el-button>
    </el-alert>
    <router-link v-if="taskId" to="/tasks">操作进行中，查看任务日志</router-link>
    <div class="security-hint">
      保护端口：{{ data.protectedPorts?.join('、') || '检测不可用' }} ·
      {{ data.externalRestrictions }}
    </div>
    <el-radio-group v-model="view" aria-label="防火墙规则视图">
      <el-radio-button value="managed">面板规则</el-radio-button>
      <el-radio-button value="actual">系统实际规则</el-radio-button>
      <el-radio-button value="docker">Docker 入站</el-radio-button>
    </el-radio-group>
  </section>
  <Resources v-if="view === 'managed'" kind="firewall" />
  <section v-if="view === 'actual'" class="surface security-panel">
    <DataList :rows="data.rules || []" searchable state-key="ufw-actual">
      <el-table-column prop="number" label="序号" width="75" />
      <el-table-column prop="destination" label="目标端口 / 地址" min-width="180" />
      <el-table-column prop="action" label="策略" width="100" />
      <el-table-column prop="direction" label="方向" width="80" />
      <el-table-column prop="source" label="来源" min-width="180" show-overflow-tooltip />
      <el-table-column prop="note" label="备注" min-width="170" show-overflow-tooltip />
      <el-table-column label="归属" width="100">
        <template #default="{ row }">{{ row.managed ? 'OpsView' : '系统规则' }}</template>
      </el-table-column>
    </DataList>
    <el-collapse>
      <el-collapse-item title="原始状态与策略诊断">
        <pre class="log-box">{{ data.raw }}</pre>
        <span>系统已有规则只读展示；面板创建的规则在“面板规则”中维护。</span>
      </el-collapse-item>
    </el-collapse>
  </section>
  <section v-if="view === 'docker'" class="surface security-panel">
    <div class="security-hint">
      Docker 发布端口的入站限制使用 DOCKER-USER，与宿主机 UFW 分开管理。请在面板规则中选择 Docker
      入站。
    </div>
    <el-tabs>
      <el-tab-pane label="IPv4">
        <pre class="log-box">{{
          data.dockerRules || '未检测到 DOCKER-USER，请检查 Docker 引擎与网络后端。'
        }}</pre>
      </el-tab-pane>
      <el-tab-pane label="IPv6">
        <pre class="log-box">{{
          data.dockerIPv6Rules || '未检测到 IPv6 DOCKER-USER；不代表 IPv6 已被阻断。'
        }}</pre>
      </el-tab-pane>
    </el-tabs>
  </section>
  <el-dialog
    v-model="modeOpen"
    :title="data.enabled ? '关闭防火墙' : '启用防火墙'"
    width="560px"
    :close-on-click-modal="false"
  >
    <el-alert
      :title="
        data.enabled
          ? '关闭 UFW 会扩大主机的网络暴露范围，Docker 规则保持独立。120 秒未确认会恢复。'
          : '保留现有规则，并为实际 SSH 与面板端口添加指定来源的放行规则。请同时检查云安全组，120 秒未确认会恢复。'
      "
      type="warning"
      :closable="false"
    />
    <el-form class="security-form" label-width="110px">
      <el-form-item v-if="!data.enabled" label="放行来源">
        <el-input v-model="source" placeholder="面板和 SSH 客户端 IP/CIDR" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="acknowledged">已了解影响，并保留现有 SSH 会话</el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="modeOpen = false">取消</el-button>
      <el-button
        type="primary"
        :loading="busy"
        :disabled="!acknowledged || (!data.enabled && !source)"
        @click="changeMode"
      >
        应用并等待确认
      </el-button>
    </template>
  </el-dialog>
</template>
