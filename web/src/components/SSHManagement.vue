<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import DataList from './DataList.vue'
import { usePageState } from '../composables/pageState'
import { usePageQuery } from '../composables/pageQuery'
import { useSecurityActions } from '../composables/securityActions'
import { errorText, type Config } from '../lib/api'
const tab = usePageState('ssh-tab', 'settings')
const { data, busy: reading, error, load } = usePageQuery<Config>('/system/ssh', {})
const {
  data: logs,
  error: logsError,
  load: loadLogs,
} = usePageQuery<Config>(() => (tab.value === 'logs' ? '/system/ssh/logs' : ''), {})
const { busy, taskId, run } = useSecurityActions(() => load(true))
const editOpen = ref(false),
  keyOpen = ref(false),
  key = ref(''),
  source = ref(''),
  acknowledged = ref(false)
const form = ref<Config>({}),
  revision = ref(''),
  ports = ref('')
const pending = computed(() => Boolean(data.value.pending?.deadline))
function openKey() {
  key.value = ''
  keyOpen.value = true
}
function edit() {
  form.value = { ...data.value.settings }
  ports.value = data.value.settings.ports.join(', ')
  revision.value = data.value.revision
  acknowledged.value = false
  editOpen.value = true
}
async function save() {
  const values = ports.value.split(/[,，\s]+/).filter(Boolean)
  if (!values.length || values.some((p) => !/^\d+$/.test(p) || +p < 1 || +p > 65535)) {
    ElMessage.error('请填写有效端口，多个端口用逗号分隔')
    return
  }
  if (
    await run('ssh-apply', {
      revision: revision.value,
      settings: { ...form.value, ports: values.map(Number) },
      source: source.value,
      confirmedImpact: acknowledged.value,
    })
  )
    editOpen.value = false
}
async function confirm() {
  try {
    await ElMessageBox.confirm(
      '请使用新的端口与认证方式另开终端完成 SSH 登录。确认后将移除临时保留的旧监听端口，已有防火墙规则保留。',
      '确认 SSH 新连接',
      { confirmButtonText: '已用新配置登录', cancelButtonText: '尚未验证', type: 'warning' },
    )
    await run('ssh-confirm', { connected: true }, false)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function deleteKey(row: Config) {
  try {
    await ElMessageBox.confirm(
      `删除 root 公钥 ${row.fingerprint}？请先验证其他公钥可以登录，现有连接不会断开。`,
      '删除公钥',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
    await run('ssh-key-delete', {
      fingerprint: row.fingerprint,
      revision: data.value.keys.revision,
      confirmedImpact: true,
    })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function addKey() {
  if (
    await run('ssh-key-add', {
      publicKey: key.value,
      revision: data.value.keys.revision,
      confirmedImpact: true,
    })
  ) {
    keyOpen.value = false
    key.value = ''
  }
}
</script>
<template>
  <section class="surface security-panel" v-loading="reading">
    <div class="page-toolbar">
      <el-radio-group v-model="tab" aria-label="SSH 管理视图">
        <el-radio-button value="settings">连接设置</el-radio-button>
        <el-radio-button value="keys">公钥管理</el-radio-button>
        <el-radio-button value="logs">登录记录</el-radio-button>
      </el-radio-group>
      <div class="page-toolbar-left">
        <el-tag :type="data.editable ? 'success' : 'info'">
          {{ data.editable ? 'SSH 运行中' : '只读诊断' }}
        </el-tag>
        <el-button :loading="reading" @click="load(true)">检查</el-button>
      </div>
    </div>
    <el-alert
      v-if="error || data.reason"
      :title="error || data.reason"
      type="warning"
      :closable="false"
    />
    <el-alert v-if="pending" type="warning" :closable="false" class="security-pending">
      <template #title>
        修改待确认 · {{ new Date(data.pending.deadline * 1000).toLocaleTimeString() }} 超时回退
      </template>
      <p v-if="data.pending.rollbackError">回退待重试：{{ data.pending.rollbackError }}</p>
      <el-button type="primary" :disabled="busy" @click="confirm">
        已重新 SSH 登录，确认保留
      </el-button>
      <el-button :disabled="busy" @click="run('ssh-rollback', {}, false)">立即回退</el-button>
    </el-alert>
    <router-link v-if="taskId" to="/tasks">操作进行中，查看任务日志</router-link>
    <template v-if="tab === 'settings' && data.settings">
      <div class="security-hint">全局配置；Match、PAM 与账号限制可能影响实际登录。</div>
      <div class="security-setting">
        <strong>监听端口</strong>
        <span>{{ data.settings.ports.join('、') }}</span>
        <el-button type="primary" :disabled="!data.editable || pending || busy" @click="edit">
          修改连接设置
        </el-button>
      </div>
      <div class="security-setting">
        <strong>密码认证</strong>
        <span>{{ data.settings.password ? '允许' : '关闭' }}</span>
      </div>
      <div class="security-setting">
        <strong>公钥认证</strong>
        <span>{{ data.settings.publicKey ? '允许' : '关闭' }}</span>
      </div>
      <div class="security-setting">
        <strong>root 登录</strong>
        <span>
          {{
            { yes: '允许', no: '禁止', 'prohibit-password': '仅公钥等非密码方式' }[
              data.settings.rootLogin as string
            ] || data.settings.rootLogin
          }}
        </span>
      </div>
      <div class="security-setting">
        <strong>认证限制</strong>
        <span>
          最多 {{ data.settings.maxAuthTries }} 次 · 登录等待 {{ data.settings.loginGraceTime }} 秒
        </span>
      </div>
      <div class="security-setting">
        <strong>连接保活</strong>
        <span>
          {{ data.settings.clientAliveInterval }} 秒 · {{ data.settings.clientAliveCountMax }} 次
        </span>
      </div>
      <el-collapse>
        <el-collapse-item title="在线会话与防爆破诊断">
          <pre class="log-box">{{ data.sessions || '暂无终端会话' }}</pre>
          <pre class="log-box">{{
            data.fail2ban || '未检测到可用的 fail2ban sshd jail；面板不会自动覆盖现有防爆破配置。'
          }}</pre>
        </el-collapse-item>
      </el-collapse>
    </template>
    <template v-if="tab === 'keys'">
      <div class="page-toolbar">
        <span>{{ data.keys?.scope || 'root 公钥' }}</span>
        <el-button
          type="primary"
          :disabled="!data.keys?.editable || pending || busy"
          @click="openKey"
        >
          添加公钥
        </el-button>
      </div>
      <el-alert v-if="data.keys?.reason" :title="data.keys.reason" type="info" :closable="false" />
      <DataList :rows="data.keys?.rows || []" state-key="ssh-keys" searchable>
        <el-table-column prop="type" label="类型" width="170" />
        <el-table-column prop="fingerprint" label="指纹" min-width="340" show-overflow-tooltip />
        <el-table-column prop="comment" label="备注" min-width="150" show-overflow-tooltip />
        <el-table-column label="限制" width="90">
          <template #default="{ row }">{{ row.restricted ? '含选项' : '无' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button
              link
              type="danger"
              :disabled="pending || busy || !data.keys?.editable"
              @click="deleteKey(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
      </DataList>
    </template>
    <template v-if="tab === 'logs'">
      <div class="page-toolbar">
        <span>{{ logs.notice }}</span>
        <el-button @click="loadLogs(true)">刷新记录</el-button>
      </div>
      <el-alert v-if="logsError" :title="logsError" type="error" :closable="false" />
      <DataList :rows="logs.rows || []" searchable state-key="ssh-logins">
        <el-table-column prop="status" label="结果" width="90" />
        <el-table-column prop="detail" label="登录记录" min-width="650" show-overflow-tooltip />
      </DataList>
      <el-collapse>
        <el-collapse-item title="最近 SSH 原始日志">
          <pre class="log-box">{{ logs.content }}</pre>
        </el-collapse-item>
      </el-collapse>
    </template>
  </section>
  <AppDialog v-model="editOpen" title="SSH 连接设置" width="640px" :close-on-click-modal="false">
    <el-alert :title="data.matchWarning" type="warning" :closable="false" />
    <el-form label-width="135px" class="security-form">
      <el-form-item label="监听端口">
        <el-input v-model="ports" placeholder="例如 555, 2222" />
      </el-form-item>
      <el-form-item label="密码认证"><el-switch v-model="form.password" /></el-form-item>
      <el-form-item label="公钥认证"><el-switch v-model="form.publicKey" /></el-form-item>
      <el-form-item label="root 登录">
        <el-select v-model="form.rootLogin">
          <el-option label="允许" value="yes" />
          <el-option label="仅非密码认证" value="prohibit-password" />
          <el-option label="禁止（先确认其他账号可登录）" value="no" />
        </el-select>
      </el-form-item>
      <el-form-item label="认证尝试次数">
        <el-input-number v-model="form.maxAuthTries" :min="1" :max="20" />
      </el-form-item>
      <el-form-item label="登录等待（秒）">
        <el-input-number v-model="form.loginGraceTime" :min="10" :max="600" />
      </el-form-item>
      <el-form-item label="保活间隔（秒）">
        <el-input-number v-model="form.clientAliveInterval" :min="0" :max="3600" />
        <span>0 表示关闭探测</span>
      </el-form-item>
      <el-form-item label="保活失败次数">
        <el-input-number v-model="form.clientAliveCountMax" :min="1" :max="10" />
      </el-form-item>
      <el-form-item label="UFW 放行来源">
        <el-input v-model="source" placeholder="SSH 客户端 IP/CIDR，UFW 已启用时必填" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="acknowledged">
          已保留现有 SSH 会话，并确认云安全组与认证影响
        </el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="editOpen = false">取消</el-button>
      <el-button type="primary" :disabled="!acknowledged" :loading="busy" @click="save">
        校验并应用
      </el-button>
    </template>
  </AppDialog>
  <AppDialog size="small" v-model="keyOpen" title="添加 root 公钥" width="600px" @closed="key = ''">
    <el-input
      v-model="key"
      type="textarea"
      :rows="5"
      placeholder="ssh-ed25519 AAAA... 备注（仅公钥）"
    />
    <template #footer>
      <el-button @click="keyOpen = false">取消</el-button>
      <el-button type="primary" :disabled="!key.trim()" :loading="busy" @click="addKey">
        添加公钥
      </el-button>
    </template>
  </AppDialog>
</template>
