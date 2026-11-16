<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, put, post, errorText, size } from '../lib/api'
import { usePanel } from '../stores/panel'
const panel = usePanel(),
  settings = ref({ name: 'OpsView', allowedCIDRs: '' }),
  password = ref({ current: '', password: '', code: '' }),
  twoFactor = ref({ password: '', code: '' }),
  secret = ref(''),
  cert = ref({ certificate: '', privateKey: '' }),
  busy = ref(false),
  metadataBackups = ref<any[]>([])
async function save() {
  busy.value = true
  try {
    await put('/settings', settings.value)
    ElMessage.success('设置已保存')
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
async function changePassword() {
  try {
    await post('/auth/password', password.value)
    await panel.session()
    ElMessage.success('密码已更新，请重新登录')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function totp(action: string) {
  try {
    const data = await post('/auth/two-factor', { action, ...twoFactor.value })
    if (action === 'prepare') secret.value = data.secret
    else {
      await panel.session()
      ElMessage.success('双因素认证已更新，请重新登录')
    }
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function certificate() {
  try {
    await ElMessageBox.confirm(
      '新证书将在后续 TLS 连接中生效。请确认覆盖你的面板访问域名。',
      '替换面板证书',
      { confirmButtonText: '应用', cancelButtonText: '取消' },
    )
    await post('/settings/certificate', cert.value)
    cert.value = { certificate: '', privateKey: '' }
    ElMessage.success('面板证书已更新')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function loadBackups() {
  metadataBackups.value = await api('/settings/backups')
}
async function backupConfig() {
  try {
    await post('/settings/backup')
    ElMessage.success('面板配置备份任务已提交，请在任务完成后刷新')
    await panel.refresh()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
onMounted(async () => {
  try {
    settings.value = { ...settings.value, ...(await api('/settings')) }
    await loadBackups()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
})
</script>
<template>
  <div class="settings-grid">
    <section class="surface settings-card">
      <h3>基本设置与访问控制</h3>
      <p>IP 限制保存前会检查当前连接，避免误将自己锁在面板之外。</p>
      <el-form label-position="top">
        <el-form-item label="面板名称"><el-input v-model="settings.name" /></el-form-item>
        <el-form-item label="允许访问的 IP 网段（留空不限制）">
          <el-input
            v-model="settings.allowedCIDRs"
            type="textarea"
            :rows="4"
            placeholder="192.0.2.1/32&#10;2001:db8::/32"
          />
        </el-form-item>
        <el-button type="primary" :loading="busy" @click="save">保存设置</el-button>
      </el-form>
    </section>
    <section class="surface settings-card">
      <h3>双因素认证</h3>
      <p>使用支持 TOTP 的验证器。开启或关闭后，现有登录会话全部失效。</p>
      <div class="notice">当前状态：{{ panel.auth.twoFactorEnabled ? '已开启' : '未开启' }}</div>
      <el-form label-position="top">
        <el-form-item label="当前管理员密码">
          <el-input v-model="twoFactor.password" type="password" show-password />
        </el-form-item>
        <el-form-item v-if="secret" label="验证器密钥">
          <el-input :model-value="secret" readonly />
          <small class="form-help">请手动添加到验证器，然后输入生成的验证码完成绑定。</small>
        </el-form-item>
        <el-form-item v-if="secret || panel.auth.twoFactorEnabled" label="六位验证码">
          <el-input v-model="twoFactor.code" maxlength="6" inputmode="numeric" />
        </el-form-item>
        <el-button
          v-if="!secret && !panel.auth.twoFactorEnabled"
          type="primary"
          @click="totp('prepare')"
        >
          开始绑定
        </el-button>
        <el-button v-if="secret" type="primary" @click="totp('confirm')">确认并开启</el-button>
        <el-button v-if="panel.auth.twoFactorEnabled" type="danger" plain @click="totp('disable')">
          关闭双因素认证
        </el-button>
      </el-form>
    </section>
    <section class="surface settings-card">
      <h3>修改管理员密码</h3>
      <p>新密码至少 12 位，最多 72 字节。修改后需重新登录。</p>
      <el-form label-position="top">
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
        <el-form-item v-if="panel.auth.twoFactorEnabled" label="双因素验证码">
          <el-input v-model="password.code" maxlength="6" />
        </el-form-item>
        <el-button type="primary" @click="changePassword">更新密码</el-button>
      </el-form>
    </section>
    <section class="surface settings-card">
      <h3>面板 HTTPS 证书</h3>
      <p>面板独立提供 HTTPS。默认使用本机自签名证书，可在这里上传受信任的证书与私钥。</p>
      <el-form label-position="top">
        <el-form-item label="PEM 证书链">
          <el-input
            v-model="cert.certificate"
            type="textarea"
            :rows="4"
            placeholder="-----BEGIN CERTIFICATE-----"
          />
        </el-form-item>
        <el-form-item label="PEM 私钥">
          <el-input
            v-model="cert.privateKey"
            type="textarea"
            :rows="4"
            placeholder="-----BEGIN PRIVATE KEY-----"
          />
        </el-form-item>
        <el-button type="primary" @click="certificate">验证并应用</el-button>
      </el-form>
    </section>
    <section class="surface settings-card">
      <h3>面板配置备份</h3>
      <p>
        加密保存 SQLite 一致性快照与面板证书，保留最近 7 份。请单独保管
        master.key；恢复使用本机命令写入新目录。
      </p>
      <el-button type="primary" @click="backupConfig">备份面板配置</el-button>
      <el-button @click="loadBackups">刷新</el-button>
      <div v-for="item in metadataBackups" :key="item.name" style="margin-top: 12px">
        <a :href="item.url" class="text-link">{{ item.name }}</a>
        <small class="cell-subtext">{{ size(item.size) }}</small>
      </div>
    </section>
  </div>
  <div class="notice" style="margin-top: 20px">
    本机维护命令：
    <code>opsview reset-admin</code>
    重置管理员；
    <code>opsview diagnose</code>
    导出脱敏诊断；
    <code>opsview trust-key</code>
    添加插件签名公钥。
    <code>opsview recover-access</code>
    解除访问限制；
    <code>opsview backup-config</code>
    创建加密配置备份。
  </div>
</template>
