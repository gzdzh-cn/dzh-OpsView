<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import LoadingContent from './LoadingContent.vue'
import { computed, ref, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import DataList from './DataList.vue'
import { usePageState } from '../composables/pageState'
import { usePageQuery } from '../composables/pageQuery'
import { useSecurityActions } from '../composables/securityActions'
import { post, errorText, type Config } from '../lib/api'
const emit = defineEmits(['protection'])
const tab = usePageState('ssh-tab', 'settings')
const period = usePageState('ssh-period', '24h')
const { data, busy: reading, error, load } = usePageQuery<Config>('/system/ssh', {})
const {
  data: logs,
  error: logsError,
  busy: logReading,
  load: loadLogs,
} = usePageQuery<Config>(
  () => (tab.value === 'logs' ? '/system/ssh/logs?period=' + period.value : ''),
  {},
)
const {
  data: users,
  busy: userReading,
  error: usersError,
  load: loadUsers,
} = usePageQuery<Config>(() => (tab.value === 'users' ? '/system/ssh/users' : ''), {})
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

const userOpen = ref(false),
  userBusy = ref(false),
  userError = ref(''),
  userAction = ref('create'),
  userRow = ref<Config>({}),
  userName = ref(''),
  newPassword = ref(''),
  repeatPassword = ref(''),
  adminPassword = ref(''),
  code = ref('')
const userTitle = computed(
  () =>
    ({
      create: '创建系统账号',
      password: '修改系统账号密码',
      lock: '锁定账号密码',
      unlock: '解锁账号密码',
      delete: '删除系统账号',
    })[userAction.value] || '系统账号',
)
function clearSecrets() {
  newPassword.value = ''
  repeatPassword.value = ''
  adminPassword.value = ''
  code.value = ''
}
onBeforeUnmount(clearSecrets)
function openUser(operation: string, row: Config = {}) {
  clearSecrets()
  userError.value = ''
  userAction.value = operation
  userRow.value = row
  userName.value = row.username || ''
  userOpen.value = true
}
async function saveUser() {
  if (userBusy.value) return
  if (userAction.value === 'password' && newPassword.value !== repeatPassword.value) {
    userError.value = '两次密码不一致'
    return
  }
  userBusy.value = true
  userError.value = ''
  const values = {
    username: userName.value,
    operation: userAction.value,
    revision: userRow.value.revision,
    password: newPassword.value,
    currentPassword: adminPassword.value,
    code: code.value,
    confirmedImpact: true,
  }
  clearSecrets()
  try {
    const result = await post('/system/security/ssh-user', values)
    userOpen.value = false
    await loadUsers(true)
    await load(true)
    ElMessage.success(result.notice || '账号操作已完成')
  } catch (e) {
    userError.value = errorText(e)
  } finally {
    values.password = ''
    values.currentPassword = ''
    values.code = ''
    userBusy.value = false
  }
}
async function service(operation: string) {
  try {
    await ElMessageBox.confirm(
      operation === 'stop'
        ? '停用会同时关闭开机自启，新 SSH 连接将不可用。请保留云控制台；120 秒内未确认将自动恢复。'
        : '将' + (operation === 'start' ? '启动' : '重启') + ' SSH 服务，已有配置保留。',
      'SSH 服务操作',
      { confirmButtonText: '确认执行', cancelButtonText: '取消', type: 'warning' },
    )
    await run('ssh-service', { operation, confirmedImpact: true })
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function confirmStop() {
  try {
    await ElMessageBox.confirm(
      '请先确认云控制台或备用管理通道可用。保留停用后，需要通过该通道才能重新启动 SSH。',
      '保留 SSH 停用',
      { confirmButtonText: '已验证备用通道，保留停用', cancelButtonText: '取消', type: 'warning' },
    )
    await run('ssh-service-confirm', { consoleConfirmed: true }, false)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <section class="surface security-panel">
    <div class="page-toolbar">
      <el-radio-group v-model="tab" aria-label="SSH 管理视图">
        <el-radio-button value="settings">基础设置</el-radio-button>
        <el-radio-button value="users">系统账号</el-radio-button>
        <el-radio-button value="keys">公钥管理</el-radio-button>
        <el-radio-button value="logs">登录记录</el-radio-button>
      </el-radio-group>
      <div class="page-toolbar-left">
        <el-tag :type="data.editable ? 'success' : 'info'">
          {{
            data.service
              ? data.service.running
                ? 'SSH 运行中'
                : 'SSH 已停止'
              : data.editable
                ? 'SSH 运行中'
                : '只读诊断'
          }}
        </el-tag>
        <el-button :loading="reading" @click="load(true)">检查</el-button>
      </div>
    </div>
    <LoadingContent :busy="reading">
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
      <el-alert
        v-if="data.service?.pending?.deadline"
        type="warning"
        :closable="false"
        class="security-pending"
      >
        <template #title>
          SSH 停用待确认 ·
          {{ new Date(data.service.pending.deadline * 1000).toLocaleTimeString() }} 自动恢复
        </template>
        <p v-if="data.service.pending.error">恢复待重试：{{ data.service.pending.error }}</p>
        <el-button type="primary" :disabled="busy" @click="service('start')">
          立即恢复 SSH
        </el-button>
        <el-button :disabled="busy" @click="confirmStop">已验证备用通道，保留停用</el-button>
      </el-alert>
      <template v-if="tab === 'settings' && data.settings">
        <div class="security-setting">
          <strong>SSH 服务</strong>
          <div class="row-actions">
            <el-button
              :disabled="!data.service?.controllable || busy || pending"
              :type="data.service?.running ? 'danger' : 'primary'"
              @click="service(data.service?.running ? 'stop' : 'start')"
            >
              {{ data.service?.running ? '停用 SSH' : '启动 SSH' }}
            </el-button>
            <el-button
              :disabled="
                !data.service?.running || busy || pending || !!data.service?.pending?.deadline
              "
              @click="service('restart')"
            >
              重启
            </el-button>
          </div>
          <span class="form-help">{{ data.service?.reason }}</span>
        </div>
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
            最多 {{ data.settings.maxAuthTries }} 次 · 登录等待
            {{ data.settings.loginGraceTime }} 秒
          </span>
        </div>
        <div class="security-setting">
          <strong>连接保活</strong>
          <span>
            {{ data.settings.clientAliveInterval }} 秒 · {{ data.settings.clientAliveCountMax }} 次
          </span>
        </div>
        <div class="security-setting">
          <strong>SSH 安全防护</strong>
          <span>Fail2ban · 日志触发封禁、管理白名单、记录与解封</span>
          <el-button @click="emit('protection')">配置与封禁记录</el-button>
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
      <template v-if="tab === 'users'">
        <div class="page-toolbar">
          <span class="form-help">{{ users.notice }}</span>
          <el-button type="primary" @click="openUser('create')">创建系统账号</el-button>
        </div>
        <el-alert v-if="usersError" :title="usersError" type="error" :closable="false">
          <el-button link @click="loadUsers(true)">重试</el-button>
        </el-alert>
        <LoadingContent :busy="userReading">
          <DataList :rows="users.rows || []" state-key="ssh-users" searchable>
            <el-table-column prop="username" label="用户名" min-width="120" />
            <el-table-column prop="uid" label="UID" width="80" />
            <el-table-column prop="home" label="主目录" min-width="160" show-overflow-tooltip />
            <el-table-column
              prop="shell"
              label="登录 Shell"
              min-width="150"
              show-overflow-tooltip
            />
            <el-table-column label="密码状态" width="110">
              <template #default="{ row }">{{ row.locked ? '锁定/未设置' : '已设置' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="230" fixed="right">
              <template #default="{ row }">
                <div class="row-actions">
                  <button @click="openUser('password', row)">修改密码</button>
                  <button v-if="!row.root" @click="openUser(row.locked ? 'unlock' : 'lock', row)">
                    {{ row.locked ? '解锁密码' : '锁定密码' }}
                  </button>
                  <button
                    v-if="row.managed && !row.root"
                    class="danger"
                    @click="openUser('delete', row)"
                  >
                    删除
                  </button>
                </div>
              </template>
            </el-table-column>
          </DataList>
        </LoadingContent>
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
        <el-alert
          v-if="data.keys?.reason"
          :title="data.keys.reason"
          type="info"
          :closable="false"
        />
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
          <el-select v-model="period" aria-label="登录日志时间范围">
            <el-option label="最近 24 小时" value="24h" />
            <el-option label="最近 7 天" value="7d" />
            <el-option label="最近 30 天" value="30d" />
          </el-select>
          <span>采样成功 {{ logs.success || 0 }} · 失败 {{ logs.failed || 0 }}</span>
          <el-button @click="loadLogs(true)">刷新记录</el-button>
        </div>
        <el-alert v-if="logsError" :title="logsError" type="error" :closable="false" />
        <p class="form-help">{{ logs.notice }}</p>
        <LoadingContent :busy="logReading">
          <DataList :rows="logs.rows || []" searchable state-key="ssh-logins">
            <el-table-column prop="status" label="结果" width="90" />
            <el-table-column prop="time" label="时间" min-width="190" />
            <el-table-column prop="username" label="账号" min-width="120" />
            <el-table-column prop="source" label="来源 IP" min-width="160" />
            <el-table-column prop="method" label="认证方式" width="120" />
            <el-table-column prop="detail" label="详情" min-width="240" show-overflow-tooltip />
          </DataList>
        </LoadingContent>
        <el-collapse>
          <el-collapse-item title="最近 SSH 原始日志">
            <pre class="log-box">{{ logs.content }}</pre>
          </el-collapse-item>
        </el-collapse>
      </template>
    </LoadingContent>
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
      <el-form-item label="防火墙放行来源">
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
  <AppDialog
    v-model="userOpen"
    :title="userTitle"
    width="640px"
    :close-on-click-modal="false"
    @closed="clearSecrets"
  >
    <el-alert
      title="操作真实系统账号；密码锁定不撤销公钥或现有会话，删除账号保留主目录且要求无运行进程。系统账号不会自动获得 sudo 权限。"
      type="warning"
      :closable="false"
    />
    <el-alert v-if="userError" :title="userError" type="error" :closable="false" />
    <el-form label-width="140px" @submit.prevent="saveUser">
      <el-form-item label="系统用户名">
        <el-input
          v-model="userName"
          :disabled="userAction !== 'create'"
          placeholder="小写字母、数字、下划线、短横线"
        />
      </el-form-item>
      <template v-if="userAction === 'password'">
        <el-form-item label="新系统密码">
          <el-input
            v-model="newPassword"
            type="password"
            show-password
            autocomplete="new-password"
            placeholder="至少 12 位"
          />
        </el-form-item>
        <el-form-item label="重复系统密码">
          <el-input v-model="repeatPassword" type="password" autocomplete="new-password" />
        </el-form-item>
      </template>
      <p v-if="userAction === 'create'" class="form-help">
        创建后默认密码锁定。请再设置密码，或通过主机为该账号配置公钥。
      </p>
      <el-form-item label="面板管理员密码">
        <el-input v-model="adminPassword" type="password" autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="双因素验证码">
        <el-input v-model="code" placeholder="面板启用双因素时必填" autocomplete="one-time-code" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button
        :disabled="userBusy"
        @click="
          () => {
            userOpen = false
            clearSecrets()
          }
        "
      >
        取消
      </el-button>
      <el-button
        type="primary"
        :loading="userBusy"
        :disabled="!userName || !adminPassword"
        @click="saveUser"
      >
        验证并执行
      </el-button>
    </template>
  </AppDialog>
</template>
