<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import DataList from '../components/DataList.vue'
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, put, post, errorText, size, type Config } from '../lib/api'
import { usePanel } from '../stores/panel'
const reads = new AbortController()
onBeforeUnmount(() => reads.abort())
const settingsLoading = ref(true)
const certificateStatus = ref<Config>({})
const panel = usePanel(),
  tab = usePageState('tab', 'common'),
  settings = ref<Config>({
    name: 'OpsView',
    allowedCIDRs: '',
    refreshSeconds: 15,
    sessionMinutes: 480,
    backupRetention: 7,
  }),
  runtime = ref<Config>({
    protocol: 'https',
    port: 18790,
    listenHost: '0.0.0.0',
    publicHost: '',
    entry: '',
    allowedHost: '',
  }),
  runtimeError = ref(''),
  pendingURL = ref(''),
  busy = ref(false),
  metadataBackups = ref<any[]>([]),
  password = ref({ current: '', password: '', code: '' }),
  twoFactor = ref({ password: '', code: '' }),
  secret = ref(''),
  cert = ref({ certificate: '', privateKey: '' }),
  acme = ref({ host: '', email: '', acceptTerms: false }),
  username = ref({ username: '', password: '', code: '' })
async function save() {
  busy.value = true
  try {
    await put('/settings', settings.value)
    window.dispatchEvent(new CustomEvent('opsview:preferences', { detail: settings.value }))
    ElMessage.success('设置已保存')
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
async function loadRuntime() {
  try {
    const result = await api('/settings/runtime', { signal: reads.signal })
    runtime.value = {
      ...runtime.value,
      ...result.config,
      allowedCIDRs: result.config.allowedCIDRs ?? settings.value.allowedCIDRs ?? '',
    }
    certificateStatus.value = result.certificate || {}
    acme.value.host = runtime.value.publicHost
    runtimeError.value = ''
  } catch (e) {
    if (!reads.signal.aborted) runtimeError.value = errorText(e)
  }
}
async function changeRuntime() {
  try {
    await ElMessageBox.confirm(
      `面板将使用 ${runtime.value.protocol.toUpperCase()}，TCP ${runtime.value.port}。修改后需在 120 秒内打开新地址确认，超时自动恢复。云安全组需单独放行。`,
      '修改访问设置',
      { confirmButtonText: '确认修改', cancelButtonText: '取消', type: 'warning' },
    )
    const result = await put('/settings/runtime', { config: runtime.value, confirmedImpact: true })
    pendingURL.value = result.url + '#runtime-confirm=' + result.confirmToken
    ElMessage.success('设置已暂存，请通过下面的新地址重新连接')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function changePassword() {
  try {
    await post('/auth/password', password.value)
    await panel.session()
    ElMessage.success('密码已更新，请重新登录')
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function changeUsername() {
  try {
    await post('/auth/username', username.value)
    await panel.session()
    ElMessage.success('用户名已更新，请重新登录')
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function totp(action: string) {
  try {
    const result = await post('/auth/two-factor', { action, ...twoFactor.value })
    if (action === 'prepare') secret.value = result.secret
    else {
      await panel.session()
      ElMessage.success('双因素认证已更新，请重新登录')
    }
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function certificate() {
  try {
    await ElMessageBox.confirm(
      '验证证书有效期与主机名后替换，后续 HTTPS 连接生效。',
      '上传面板证书',
      { confirmButtonText: '应用', cancelButtonText: '取消' },
    )
    await post('/settings/certificate', cert.value)
    cert.value = { certificate: '', privateKey: '' }
    ElMessage.success('证书已更新')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function issue() {
  try {
    await post('/settings/certificate/issue', acme.value)
    ElMessage.success('证书申请任务已提交，请在任务中心查看')
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function loadBackups() {
  try {
    metadataBackups.value = await api('/settings/backups', { signal: reads.signal })
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function backup() {
  try {
    await post('/settings/backup')
    await panel.refresh()
    ElMessage.success('加密备份任务已提交')
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
watch(tab, (value) => {
  if (value === 'backup') void loadBackups()
  if (['panel', 'security'].includes(value)) void loadRuntime()
})
onMounted(async () => {
  try {
    settings.value = { ...settings.value, ...(await api('/settings', { signal: reads.signal })) }
    if (tab.value === 'backup') await loadBackups()
    if (['common', 'panel', 'security'].includes(tab.value)) await loadRuntime()
  } catch (e) {
    if (!reads.signal.aborted) ElMessage.error(errorText(e))
  } finally {
    settingsLoading.value = false
  }
})
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="常用设置" name="common" />
    <el-tab-pane label="面板设置" name="panel" />
    <el-tab-pane label="安全设置" name="security" />
    <el-tab-pane label="界面设置" name="appearance" />
    <el-tab-pane label="备份恢复" name="backup" />
  </el-tabs>
  <div v-loading="settingsLoading" class="surface settings-surface">
    <template v-if="tab === 'common'">
      <div class="setting-row">
        <div>
          <strong>面板名称</strong>
          <p>显示在浏览器标题和面板工作空间中</p>
        </div>
        <el-input v-model="settings.name" maxlength="80" />
        <el-button type="primary" @click="save">保存</el-button>
      </div>
      <div class="setting-row">
        <div>
          <strong>管理员</strong>
          <p>当前登录账号</p>
        </div>
        <span>{{ panel.auth.username }}</span>
        <el-button @click="tab = 'security'">修改</el-button>
      </div>
      <div class="setting-row">
        <div>
          <strong>终端维护</strong>
          <p>通过 SSH 使用 ov 菜单恢复访问、管理服务和证书</p>
        </div>
        <code>sudo ov</code>
      </div>
    </template>
    <template v-if="tab === 'panel'">
      <el-alert v-if="runtimeError" :title="runtimeError" type="warning" :closable="false" />
      <div class="setting-row">
        <div>
          <strong>面板地址</strong>
          <p>用于安装摘要及终端登录链接</p>
        </div>
        <el-input v-model="runtime.publicHost" placeholder="域名或公网 IP" />
      </div>
      <div class="setting-row">
        <div>
          <strong>监听端口</strong>
          <p>旧防火墙规则保留，云安全组需单独配置</p>
        </div>
        <el-input-number v-model="runtime.port" :min="1" :max="65535" />
      </div>
      <div class="setting-row">
        <div>
          <strong>面板 SSL</strong>
          <p>关闭后使用 HTTP，登录凭据将以明文传输；证书文件保留</p>
        </div>
        <el-radio-group v-model="runtime.protocol">
          <el-radio-button value="https">开启 HTTPS</el-radio-button>
          <el-radio-button value="http">关闭 SSL</el-radio-button>
        </el-radio-group>
      </div>
      <div class="setting-row">
        <div>
          <strong>应用访问设置</strong>
          <p>重启后通过新地址确认，120 秒未确认自动回退</p>
        </div>
        <el-button type="primary" :disabled="!!runtimeError" @click="changeRuntime">
          保存并重新连接
        </el-button>
      </div>
      <div v-if="pendingURL" class="notice warn">
        请等待服务重启后打开：
        <a :href="pendingURL" target="_blank" rel="noopener">新面板登录地址（点击确认）</a>
        。超时会恢复原设置。
      </div>
      <h3>免费证书申请</h3>
      <p v-if="certificateStatus.expires">
        当前证书到期：{{ new Date(certificateStatus.expires).toLocaleString('zh-CN') }} · 签发者：{{
          certificateStatus.issuer
        }}
        · 自动续期：{{
          runtime.protocol === 'http' ? '已暂停' : certificateStatus.autoRenew ? '已启用' : '未启用'
        }}
      </p>
      <p class="form-help">
        支持域名和公网 IP，HTTP-01 需要公网 TCP 80 可达。申请失败保留原证书，自动续期由执行器处理。
      </p>
      <el-form label-width="130px">
        <el-form-item label="域名 / 公网 IP"><el-input v-model="acme.host" /></el-form-item>
        <el-form-item label="联系邮箱"><el-input v-model="acme.email" /></el-form-item>
        <el-form-item label="服务条款">
          <el-checkbox v-model="acme.acceptTerms">同意 Let's Encrypt ACME 服务条款</el-checkbox>
        </el-form-item>
      </el-form>
      <el-button type="primary" @click="issue">申请 / 续期证书</el-button>
      <el-divider />
      <h3>上传证书</h3>
      <el-form label-position="top">
        <el-form-item label="PEM 证书链">
          <el-input v-model="cert.certificate" type="textarea" :rows="4" />
        </el-form-item>
        <el-form-item label="PEM 私钥">
          <el-input v-model="cert.privateKey" type="textarea" :rows="4" />
        </el-form-item>
      </el-form>
      <el-button @click="certificate">验证并应用</el-button>
    </template>
    <template v-if="tab === 'security'">
      <div class="setting-row">
        <div>
          <strong>安全入口</strong>
          <p>8–64 位字母、数字、横线或下划线；留空取消</p>
        </div>
        <el-input v-model="runtime.entry" placeholder="例如 my_secure_entry" />
      </div>
      <div class="setting-row">
        <div>
          <strong>绑定访问域名</strong>
          <p>启用后，仅接受该主机名访问面板</p>
        </div>
        <el-input v-model="runtime.allowedHost" placeholder="panel.example.com" />
        <el-button :disabled="!!runtimeError" @click="changeRuntime">应用并确认</el-button>
      </div>
      <div class="setting-row">
        <div>
          <strong>IP 访问限制</strong>
          <p>每行一个 CIDR，必须包含当前连接 IP；留空不限制</p>
        </div>
        <el-input v-model="runtime.allowedCIDRs" type="textarea" :rows="3" />
        <el-button :disabled="!!runtimeError" @click="changeRuntime">应用并确认</el-button>
      </div>
      <div class="setting-row">
        <div>
          <strong>登录会话有效期</strong>
          <p>15 分钟至 7 天，下次登录生效</p>
        </div>
        <el-input-number v-model="settings.sessionMinutes" :min="15" :max="10080" />
        <span>分钟</span>
        <el-button @click="save">保存</el-button>
      </div>
      <h3>修改管理员用户名</h3>
      <el-form label-width="130px">
        <el-form-item label="新用户名"><el-input v-model="username.username" /></el-form-item>
        <el-form-item label="当前密码">
          <el-input v-model="username.password" type="password" show-password />
        </el-form-item>
        <el-form-item v-if="panel.auth.twoFactorEnabled" label="验证码">
          <el-input v-model="username.code" maxlength="6" />
        </el-form-item>
      </el-form>
      <el-button @click="changeUsername">修改用户名并退出</el-button>
      <el-divider />
      <h3>修改密码</h3>
      <el-form label-width="130px">
        <el-form-item label="当前密码">
          <el-input v-model="password.current" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input
            v-model="password.password"
            type="password"
            show-password
            autocomplete="new-password"
          />
        </el-form-item>
        <el-form-item v-if="panel.auth.twoFactorEnabled" label="验证码">
          <el-input v-model="password.code" maxlength="6" />
        </el-form-item>
      </el-form>
      <el-button type="primary" @click="changePassword">修改密码并退出</el-button>
      <el-divider />
      <h3>
        双因素认证
        <el-tag>{{ panel.auth.twoFactorEnabled ? '已开启' : '未开启' }}</el-tag>
      </h3>
      <el-form label-width="130px">
        <el-form-item label="当前密码">
          <el-input v-model="twoFactor.password" type="password" show-password />
        </el-form-item>
        <el-form-item v-if="secret" label="验证器密钥">
          <el-input :model-value="secret" readonly />
        </el-form-item>
        <el-form-item v-if="secret || panel.auth.twoFactorEnabled" label="验证码">
          <el-input v-model="twoFactor.code" maxlength="6" />
        </el-form-item>
      </el-form>
      <el-button v-if="!secret && !panel.auth.twoFactorEnabled" @click="totp('prepare')">
        开始绑定
      </el-button>
      <el-button v-if="secret" type="primary" @click="totp('confirm')">确认开启</el-button>
      <el-button v-if="panel.auth.twoFactorEnabled" type="danger" plain @click="totp('disable')">
        关闭双因素认证
      </el-button>
    </template>
    <template v-if="tab === 'appearance'">
      <div class="setting-row">
        <div>
          <strong>数据刷新间隔</strong>
          <p>概览、资源及任务列表的自动刷新周期</p>
        </div>
        <el-select v-model="settings.refreshSeconds">
          <el-option v-for="n in [5, 15, 30, 60]" :key="n" :value="n" :label="n + ' 秒'" />
        </el-select>
        <el-button @click="save">保存</el-button>
      </div>
    </template>
    <template v-if="tab === 'backup'">
      <div class="setting-row">
        <div>
          <strong>备份保留份数</strong>
          <p>面板元数据、运行配置与证书加密备份，主密钥请单独保存</p>
        </div>
        <el-input-number v-model="settings.backupRetention" :min="1" :max="30" />
        <el-button @click="save">保存</el-button>
      </div>
      <div class="settings-actions">
        <el-button type="primary" @click="backup">立即备份面板配置</el-button>
        <el-button @click="loadBackups">刷新备份</el-button>
      </div>
      <DataList state-key="Settings-1" :rows="metadataBackups">
        <el-table-column label="备份文件">
          <template #default="{ row }">
            <a :href="row.url">{{ row.name }}</a>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="120">
          <template #default="{ row }">{{ size(row.size) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100">
          <template #default="{ row }"><a :href="row.url">下载</a></template>
        </el-table-column>
      </DataList>
      <p class="form-help">恢复命令将备份解密至新的目录，核对后再切换，保留当前数据目录。</p>
    </template>
  </div>
</template>
<style scoped>
.settings-surface {
  padding: 8px 28px 28px;
}
.setting-row {
  display: flex;
  gap: 18px;
  align-items: center;
  min-height: 100px;
  border-bottom: 1px solid #eef1f5;
  padding: 20px 0;
}
.setting-row > div:first-child {
  width: 340px;
  flex-shrink: 0;
}
.setting-row strong {
  font-size: 14px;
}
.setting-row p {
  font-size: 12px;
  color: #94a3b8;
  line-height: 1.7;
  margin: 6px 0;
}
.setting-row > .el-input,
.setting-row > .el-textarea {
  max-width: 350px;
}
.setting-row > .el-select {
  width: 180px;
}
.settings-surface h3 {
  margin-top: 30px;
}
.settings-surface > .el-form {
  max-width: 680px;
}
.settings-actions {
  margin: 22px 0;
}
@media (max-width: 800px) {
  .setting-row {
    flex-wrap: wrap;
  }
  .setting-row > div:first-child {
    width: 100%;
  }
  .settings-surface {
    padding: 12px 16px;
  }
}
</style>
