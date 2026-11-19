<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import SettingRow from '../components/SettingRow.vue'
import AppDialog from '../components/AppDialog.vue'
import SecurityCredentials from '../components/SecurityCredentials.vue'
import { identity, saveJSON } from '../lib/identity'
import { applyAppearance } from '../lib/appearance'
const NotificationManager = defineAsyncComponent(
  () => import('../components/NotificationManager.vue'),
)
const BackupRecords = defineAsyncComponent(() => import('../components/BackupRecords.vue'))
import { computed, defineAsyncComponent, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, put, post, errorText, type Config } from '../lib/api'
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
type Field = {
  key: string
  label: string
  group: string
  help: string
  type?: string
  min?: number
  max?: number
  options?: { value: any; label: string }[]
}
const region = ref<Config>({})
async function regionStatus() {
  try {
    region.value = await api('/settings/regions')
  } catch (e) {
    region.value = { reason: errorText(e) }
  }
}
async function updateRegions() {
  try {
    await post('/settings/regions')
    ElMessage.success('更新任务已提交，完成后重新检查本地数据')
  } catch (e) {
    editError.value = errorText(e)
  }
}
const fields: Field[] = [
  { key: 'name', label: '面板名称', group: 'panel', help: '浏览器标题和登录页名称' },
  { key: 'sidebarTitle', label: '侧栏标题', group: 'panel', help: '显示于左侧导航顶部' },
  {
    key: 'siteDirectory',
    label: '默认建站目录',
    group: 'panel',
    help: '仅影响新站点，不迁移已有网站',
  },
  {
    key: 'backupDirectory',
    label: '默认备份目录',
    group: 'backup',
    help: '仅影响新备份任务；执行前检查权限和空间',
  },
  { key: 'backupStorageId', label: '面板备份存储', group: 'backup', help: '保留本地加密副本，可同时上传已有 S3 存储', type: 'storage' },
  {
    key: 'timezone',
    label: '时区',
    group: 'panel',
    help: '新计划使用此时区；已有计划保留各自时区',
  },
  {
    key: 'refreshSeconds',
    label: '刷新频率',
    group: 'panel',
    help: '浏览器后台暂停轮询',
    type: 'select',
    options: [5, 15, 30, 60].map((value) => ({ value, label: value + ' 秒' })),
  },
  {
    key: 'idleOptimization',
    label: '空闲优化',
    group: 'panel',
    help: '仅暂停浏览器非必要采集，不影响计划和告警',
    type: 'switch',
  },
  {
    key: 'webDisabled',
    label: '关闭面板网页入口',
    group: 'panel',
    help: '计划与告警继续运行；通过 ov panel on 恢复',
    type: 'switch',
  },
  {
    key: 'sessionMinutes',
    label: '绝对会话期限',
    group: 'security',
    help: '分钟，下次登录生效，最长七天',
    type: 'number',
    min: 15,
    max: 10080,
  },
  {
    key: 'idleMinutes',
    label: '无操作超时',
    group: 'security',
    help: '分钟，后台自动轮询不会延长此期限',
    type: 'number',
    min: 5,
    max: 10080,
  },
  {
    key: 'passwordMinLength',
    label: '密码最小长度',
    group: 'security',
    help: '新密码必须满足，默认不强迫现有账号改密',
    type: 'number',
    min: 12,
    max: 64,
  },
  {
    key: 'passwordRequireMixed',
    label: '密码混合字符',
    group: 'security',
    help: '要求大小写字母、数字和符号',
    type: 'switch',
  },
  {
    key: 'passwordExpiryDays',
    label: '密码有效期',
    group: 'security',
    help: '天，0 关闭；启用后到期账号须先修改密码',
    type: 'number',
    min: 0,
    max: 1825,
  },
  {
    key: 'unauthenticatedStatus',
    label: '未认证访问响应',
    group: 'security',
    help: '不影响已登录账号；请保留本机恢复方式',
    type: 'select',
    options: [
      { value: 0, label: '正常登录' },
      { value: 404, label: '404' },
      { value: 403, label: '403' },
    ],
  },
  {
    key: 'basicEnabled',
    label: 'BasicAuth',
    group: 'security',
    help: 'HTTP 基础认证，启用不替代管理员登录',
    type: 'basic',
  },
  {
    key: 'clientCertificateEnabled',
    label: '客户端证书认证',
    group: 'security',
    help: '先生成并导入客户端证书，再启用；仅 HTTPS',
    type: 'switch',
  },
  {
    key: 'apiEnabled',
    label: '开放 API',
    group: 'security',
    help: '默认关闭，HTTPS 请求头传递凭据，逐次校验范围',
    type: 'switch',
  },
  {
    key: 'userAgentAllow',
    label: 'User-Agent 限制',
    group: 'security',
    help: '正则表达式，须包含当前浏览器；留空关闭',
  },
  {
    key: 'loginCountries',
    label: '允许登录地区',
    group: 'security',
    help: '使用本地国家地址数据；留空关闭',
    type: 'countries',
  },
  {
    key: 'theme',
    label: '界面模式',
    group: 'appearance',
    help: '跟随系统、浅色或深色',
    type: 'select',
    options: [
      { value: 'auto', label: '跟随系统' },
      { value: 'light', label: '浅色' },
      { value: 'dark', label: '深色' },
    ],
  },
  {
    key: 'primaryColor',
    label: '主题色',
    group: 'appearance',
    help: '统一按钮、标签和高亮颜色',
    type: 'color',
  },
  {
    key: 'fontSize',
    label: '统一字号',
    group: 'appearance',
    help: '12–18 像素',
    type: 'number',
    min: 12,
    max: 18,
  },
  {
    key: 'hiddenMenus',
    label: '侧栏菜单',
    group: 'appearance',
    help: '勾选需要隐藏的菜单；设置和退出始终保留',
    type: 'menus',
  },
  {
    key: 'loginLogo',
    label: '登录 Logo',
    group: 'appearance',
    help: 'PNG/JPEG/WebP，最大 1.5 MiB',
    type: 'image',
  },
  {
    key: 'loginBackground',
    label: '登录背景',
    group: 'appearance',
    help: 'PNG/JPEG/WebP，最大 1.5 MiB',
    type: 'image',
  },
  {
    key: 'favicon',
    label: '浏览器图标',
    group: 'appearance',
    help: 'PNG/WebP 图片',
    type: 'image',
  },
  {
    key: 'backupRetention',
    label: '面板备份保留数量',
    group: 'backup',
    help: '新备份成功后才清理旧备份，主密钥独立保管',
    type: 'number',
    min: 1,
    max: 30,
  },
]
const menuOptions = [
  ['/', '概览'],
  ['/sites', '网站'],
  ['/databases', '数据库'],
  ['/docker', 'Docker'],
  ['/software', '插件市场'],
  ['/files', '文件'],
  ['/backups', '备份与计划'],
  ['/system', '系统与安全'],
]
const groupRevisions = ref<Record<string, number>>({}),
  field = ref<Field>(),
  editor = ref(false),
  editValue = ref<any>(),
  editError = ref(''),
  basicPassword = ref(''),
  basicUsername = ref(''),
  unknownCountry = ref('deny'),
  countriesText = ref(''),
  securityMode = ref(''),
  mode = ref(''),
  confirmToken = ref('')
const shownFields = computed(() =>
  fields.filter((f) =>
    tab.value === 'common'
      ? ['name', 'refreshSeconds', 'siteDirectory', 'backupDirectory', 'sessionMinutes'].includes(
          f.key,
        )
      : tab.value === 'panel'
        ? f.group === 'panel' && f.key !== 'refreshSeconds'
        : tab.value === 'appearance'
          ? f.group === 'appearance' || f.key === 'refreshSeconds'
          : f.group === tab.value,
  ),
)
function valueLabel(f: Field) {
  const v = settings.value[f.key]
  if (f.type === 'switch' || f.type === 'basic') return v ? '已开启' : '已关闭'
  if (f.type === 'storage') return v ? panel.resources.find(r => r.id === v)?.name || '存储已移除' : '仅本地'
  if (f.type === 'image') return v ? '已上传' : '默认'
  if (Array.isArray(v)) return v.length ? v.join('、') : '默认'
  return f.options?.find((o) => o.value === v)?.label ?? v ?? '—'
}
async function loadGroup(group: string) {
  const r = await api('/settings/groups/' + group, { signal: reads.signal })
  Object.assign(settings.value, r.config)
  groupRevisions.value[group] = r.revision
  return r
}
async function edit(f: Field) {
  field.value = f
  editError.value = ''
  basicPassword.value = ''
  editor.value = true
  busy.value = true
  try {
    await loadGroup(f.group)
    editValue.value = structuredClone(settings.value[f.key])
    basicUsername.value = settings.value.basicUsername || ''
    countriesText.value = (settings.value.loginCountries || []).join(' ')
    unknownCountry.value = settings.value.unknownCountry || 'deny'
    if (f.type === 'countries') void regionStatus()
  } catch (e) {
    editError.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function save() {
  if (!field.value || busy.value) return
  busy.value = true
  editError.value = ''
  try {
    const f = field.value
    let patch: Config = { [f.key]: editValue.value }
    if (f.type === 'basic') patch.basicUsername = basicUsername.value
    if (f.type === 'countries') {
      patch.loginCountries = countriesText.value
        .toUpperCase()
        .split(/[\s,]+/)
        .filter(Boolean)
      patch.unknownCountry = unknownCountry.value
    }
    if (f.key === 'timezone' && editValue.value !== settings.value.timezone)
      await ElMessageBox.confirm(
        '已有计划保留各自时区，新计划使用新的默认时区。请核对执行时间。',
        '更改时区',
        { confirmButtonText: '继续', cancelButtonText: '取消' },
      )
    if (f.key === 'webDisabled' && editValue.value)
      await ElMessageBox.confirm(
        '关闭后无法通过网页重新开启。请确认能够 SSH 登录并执行 ov panel on；计划、备份和告警继续运行。',
        '关闭网页入口',
        { type: 'warning', confirmButtonText: '关闭入口', cancelButtonText: '取消' },
      )
    const auth = f.group === 'security' || f.key === 'webDisabled' ? await identity() : {}
    const result = await api('/settings/groups/' + f.group, {
      method: 'PATCH',
      body: JSON.stringify({
        patch,
        revision: groupRevisions.value[f.group],
        ...auth,
        basicPassword: basicPassword.value,
      }),
    })
    Object.assign(settings.value, patch)
    groupRevisions.value[f.group] = result.revision
    applyAppearance(settings.value)
    window.dispatchEvent(new CustomEvent('opsview:preferences', { detail: settings.value }))
    confirmToken.value = result.confirmToken || ''
    editor.value = false
    ElMessage.success('设置已保存')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') editError.value = errorText(e)
  } finally {
    busy.value = false
  }
}
function accessConfirmURL() {
  return (
    location.origin +
    location.pathname +
    '#preference-confirm=' +
    encodeURIComponent(confirmToken.value)
  )
}
async function confirmAccess() {
  try {
    await post('/settings/confirm', { token: confirmToken.value })
    confirmToken.value = ''
    ElMessage.success('访问设置已确认')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function clearModeSecrets(){password.value={current:'',password:'',code:''};username.value.password='';twoFactor.value.password='';secret.value='';cert.value={certificate:'',privateKey:''}}
function openMode(value: string) {
  mode.value = value
  editError.value = ''
}
async function imageFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (
    file.size > 1.5 * 1024 * 1024 ||
    !['image/png', 'image/jpeg', 'image/webp'].includes(file.type)
  ) {
    editError.value = '请选择 1.5 MiB 以内的 PNG/JPEG/WebP'
    return
  }
  const reader = new FileReader()
  reader.onload = () => (editValue.value = reader.result)
  reader.readAsDataURL(file)
}
function previewTheme() {
  if (field.value) applyAppearance({ [field.value.key]: editValue.value })
}
function closeEditor() {
  basicPassword.value = ''
  applyAppearance(settings.value)
}
async function importTheme(event: Event) {
  try {
    const file = (event.target as HTMLInputElement).files?.[0]
    if (!file) return
    const value = JSON.parse(await file.text())
    if (value.format !== 'opsview-theme' || value.version !== 1)
      throw new Error('仅支持 OpsView 主题格式')
    await loadGroup('appearance')
    const result = await api('/settings/groups/appearance', {
      method: 'PATCH',
      body: JSON.stringify({ patch: value.config, revision: groupRevisions.value.appearance }),
    })
    Object.assign(settings.value, result.config)
    applyAppearance(settings.value)
    ElMessage.success('主题已导入')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function exportTheme() {
  try {
    const r = await loadGroup('appearance')
    saveJSON({ format: 'opsview-theme', version: 1, config: r.config }, 'opsview-theme.json')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function resetTheme() {
  try {
    await ElMessageBox.confirm('恢复默认主题？', '恢复主题', {
      confirmButtonText: '恢复',
      cancelButtonText: '取消',
    })
    await loadGroup('appearance')
    const result = await api('/settings/groups/appearance', {
      method: 'PATCH',
      body: JSON.stringify({
        revision: groupRevisions.value.appearance,
        patch: {
          theme: 'auto',
          primaryColor: '#409eff',
          fontSize: 14,
          hiddenMenus: [],
          loginLogo: '',
          loginBackground: '',
          favicon: '',
        },
      }),
    })
    Object.assign(settings.value, result.config)
    applyAppearance(settings.value)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function autoBackup() {
  try {
    await post('/resources', {
      kind: 'schedule',
      name: '面板每日自动备份-' + Date.now(),
      config: {
        operation: 'panel',
        cron: '0 3 * * *',
        timezone: settings.value.timezone || 'Local',
        enabled: true,
        retentionDays: 30,
      },
    })
    ElMessage.success('每日 03:00 面板备份已创建，可在计划任务中调整')
  } catch (e) {
    ElMessage.error(errorText(e))
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
    const result = await put('/settings/runtime', {
      config: runtime.value,
      confirmedImpact: true,
      ...(await identity()),
    })
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
    applyAppearance(settings.value)
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
    <el-tab-pane label="告警通知" name="notifications" />
    <el-tab-pane label="备份恢复" name="backup" />
  </el-tabs>
  <el-alert
    v-if="confirmToken"
    title="访问设置将在 120 秒后自动回退。请在当前访问条件下重新连接并确认。"
    type="warning"
    :closable="false"
  >
    <el-button @click="confirmAccess">确认当前访问正常</el-button>
    <a :href="accessConfirmURL()" target="_blank" rel="noopener">重新打开并确认</a>
  </el-alert>
  <NotificationManager v-if="tab === 'notifications'" />
  <div v-else v-loading="settingsLoading" class="surface settings-surface">
    <SettingRow
      v-for="f in shownFields"
      :key="f.key"
      :label="f.label"
      :value="valueLabel(f)"
      :help="f.help"
      @edit="edit(f)"
    />
    <template v-if="tab === 'common'">
      <SettingRow
        label="管理员"
        :value="panel.auth.username"
        help="修改后旧会话失效"
        @edit="openMode('username')"
      />
      <SettingRow label="本机恢复" value="sudo ov" help="维护命令独立于网页入口">
        <template #action><el-tag type="info">SSH</el-tag></template>
      </SettingRow>
    </template>
    <template v-if="tab === 'panel'">
      <SettingRow
        label="面板访问"
        :value="runtime.protocol + ' · ' + runtime.listenHost + ':' + runtime.port"
        :help="runtimeError || '公网地址、IPv4/IPv6、端口及协议；120 秒连接确认'"
        @edit="
          openMode('runtime'); loadRuntime()
        "
      />
      <SettingRow
        label="面板证书"
        :value="
          certificateStatus.expires ? new Date(certificateStatus.expires).toLocaleString() : '—'
        "
        :help="certificateStatus.issuer || '申请、上传及续期'"
        @edit="openMode('certificate')"
      />
      <SettingRow
        label="时间同步"
        value="系统时间服务"
        help="通过受控计划任务同步，不修改系统时间命令"
      >
        <template #action><router-link to="/backups">计划任务</router-link></template>
      </SettingRow>
    </template>
    <template v-if="tab === 'security'">
      <SettingRow
        label="安全入口及来源"
        :value="runtime.entry || '未设置安全入口'"
        help="安全入口、访问域名及 IP/CIDR 限制"
        @edit="
          openMode('runtime'); loadRuntime()
        "
      />
      <SettingRow label="管理员用户名" :value="panel.auth.username" @edit="openMode('username')" />
      <SettingRow label="管理员密码" value="已设置" @edit="openMode('password')" />
      <SettingRow
        label="双因素认证"
        :value="panel.auth.twoFactorEnabled ? '已开启' : '未开启'"
        @edit="openMode('totp')"
      />
      <SettingRow label="登录会话" value="管理在线会话" @edit="securityMode = 'sessions'" />
      <SettingRow
        label="Passkey"
        value="管理通行密钥"
        help="有效 HTTPS 域名、安全上下文及设备验证"
        @edit="securityMode = 'passkeys'"
      />
      <SettingRow
        label="客户端证书"
        value="生成并下载"
        help="私钥仅在生成时提供；请先导入再启用认证"
        @edit="securityMode = 'certificate'"
      />
      <SettingRow label="API 凭据" value="管理范围、来源及到期" @edit="securityMode = 'api'" />
      <SettingRow
        label="临时访问授权"
        value="只读 · 十分钟 · 一次兑换"
        @edit="securityMode = 'temporary'"
      />
    </template>
    <template v-if="tab === 'appearance'">
      <div class="page-toolbar">
        <el-button @click="exportTheme">导出主题</el-button>
        <label class="el-button">
          <input type="file" accept="application/json" hidden @change="importTheme" />
          导入主题
        </label>
        <el-button @click="resetTheme">恢复默认</el-button>
      </div>
    </template>
    <template v-if="tab === 'backup'">
      <SettingRow
        label="自动面板备份"
        value="计划任务统一管理"
        help="创建每日 03:00 加密备份；请避免重复计划"
      >
        <template #action>
          <el-button link type="primary" @click="autoBackup">创建计划</el-button>
          <router-link to="/backups">管理计划</router-link>
        </template>
      </SettingRow>
      <div class="page-toolbar">
        <el-button type="primary" @click="backup">立即备份面板</el-button>
        <router-link to="/backups?tab=records"><el-button>业务备份</el-button></router-link>
      </div>
      <BackupRecords panel-only />
      <p class="form-help">
        恢复需要原 master.key；业务 .ovb 需要执行器 backup.key。密钥不包含在备份包中，请通过 SSH
        单独备份并离线保管。恢复计划默认暂停，API 和临时授权失效。
      </p>
    </template>
  </div>
  <AppDialog
    v-model="editor"
    :title="field?.label || '修改设置'"
    width="720px"
    :close-on-click-modal="false"
    destroy-on-close
    @closed="closeEditor"
  >
    <el-alert v-if="editError" :title="editError" type="error" :closable="false" />
    <el-form v-if="field" v-loading="busy" label-width="120px">
      <el-form-item :label="field.label">
        <el-switch v-if="['switch', 'basic'].includes(field.type || '')" v-model="editValue" />
        <el-input-number
          v-else-if="field.type === 'number'"
          v-model="editValue"
          :min="field.min"
          :max="field.max"
        />
        <el-select v-else-if="field.type === 'select'" v-model="editValue">
          <el-option v-for="o in field.options" :key="o.value" :value="o.value" :label="o.label" />
        </el-select>
        <el-select v-else-if="field.type === 'storage'" v-model="editValue">
          <el-option label="仅本地" value="" />
          <el-option v-for="r in panel.resources.filter(r => r.kind === 'backup' && r.config.destination === 's3')" :key="r.id" :value="r.id" :label="r.name" />
        </el-select>
        <el-color-picker v-else-if="field.type === 'color'" v-model="editValue" />
        <el-checkbox-group v-else-if="field.type === 'menus'" v-model="editValue">
          <el-checkbox v-for="[path, label] in menuOptions" :key="path" :value="path">
            {{ label }}
          </el-checkbox>
        </el-checkbox-group>
        <div v-else-if="field.type === 'image'">
          <input type="file" accept="image/png,image/jpeg,image/webp" @change="imageFile" />
          <el-button link @click="editValue = ''">清除</el-button>
          <img
            v-if="editValue"
            :src="editValue"
            style="display: block; max-width: 240px; max-height: 160px"
            alt="图片预览"
          />
        </div>
        <el-input
          v-else-if="field.type === 'countries'"
          v-model="countriesText"
          placeholder="如 CN JP，留空关闭"
        />
        <el-input v-else v-model="editValue" />
      </el-form-item>
      <p class="form-help">{{ field.help }}</p>
      <template v-if="field.type === 'basic'">
        <el-form-item label="BasicAuth 用户名">
          <el-input v-model="basicUsername" autocomplete="off" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input
            v-model="basicPassword"
            type="password"
            show-password
            autocomplete="new-password"
            placeholder="至少十二字节；留空保留"
          />
        </el-form-item>
      </template>
      <el-form-item v-if="field.type === 'countries'" label="未知地区">
        <el-select v-model="unknownCountry">
          <el-option value="deny" label="拒绝" />
          <el-option value="allow" label="允许" />
        </el-select>
      </el-form-item>
      <div v-if="field.type === 'countries'">
        <p class="form-help">
          IP 归属按地址分配数据判断。当前：{{ region.country || '未知' }}；更新：{{
            region.updatedAt || region.reason || '未下载'
          }}
        </p>
        <el-button @click="updateRegions">更新地区数据</el-button>
        <el-button @click="regionStatus">检查本地数据</el-button>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="editor = false">取消</el-button>
      <el-button v-if="field?.group === 'appearance'" @click="previewTheme">预览</el-button>
      <el-button type="primary" :loading="busy" @click="save">保存</el-button>
    </template>
  </AppDialog>
  <AppDialog
    :model-value="!!mode"
    :title="
      (
        {
          runtime: '访问设置',
          certificate: '面板证书',
          username: '修改用户名',
          password: '修改密码',
          totp: '双因素认证',
        } as Record<string, string>
      )[mode]
    "
    width="760px"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="mode = ''"
    @closed="clearModeSecrets"
  >
    <el-alert
      v-if="runtimeError && mode === 'runtime'"
      :title="runtimeError"
      type="error"
      :closable="false"
    />
    <template v-if="mode === 'runtime'">
      <el-form label-width="120px">
        <el-form-item label="公网地址">
          <el-input v-model="runtime.publicHost" placeholder="域名或公网 IP" />
        </el-form-item>
        <el-form-item label="监听地址">
          <el-select v-model="runtime.listenHost" allow-create filterable>
            <el-option value="0.0.0.0" label="全部 IPv4" />
            <el-option value="::" label="全部 IPv4/IPv6" />
            <el-option value="127.0.0.1" label="仅本机 IPv4" />
            <el-option value="::1" label="仅本机 IPv6" />
          </el-select>
        </el-form-item>
        <el-form-item label="监听端口">
          <el-input-number v-model="runtime.port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="协议">
          <el-radio-group v-model="runtime.protocol">
            <el-radio value="https">HTTPS</el-radio>
            <el-radio value="http">HTTP（关闭 SSL）</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="安全入口">
          <el-input v-model="runtime.entry" placeholder="8–64 位字母、数字、横线或下划线" />
        </el-form-item>
        <el-form-item label="访问域名"><el-input v-model="runtime.allowedHost" /></el-form-item>
        <el-form-item label="IP/CIDR 限制">
          <el-input v-model="runtime.allowedCIDRs" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <p class="form-help">
        域名变化可能使既有 Passkey 无法使用，请保留密码及本机恢复。HTTP 不支持 Passkey、客户端证书及
        API 凭据。
      </p>
      <div v-if="pendingURL" class="notice warn">
        <a :href="pendingURL" target="_blank" rel="noopener">打开新地址并确认（120 秒内）</a>
      </div>
    </template>
    <template v-if="mode === 'certificate'">
      <el-form label-width="120px">
        <el-form-item label="域名 / 公网 IP"><el-input v-model="acme.host" /></el-form-item>
        <el-form-item label="联系邮箱"><el-input v-model="acme.email" /></el-form-item>
        <el-form-item label="服务条款">
          <el-checkbox v-model="acme.acceptTerms">同意 Let's Encrypt 服务条款</el-checkbox>
        </el-form-item>
      </el-form>
      <el-button @click="issue">申请 / 续期</el-button>
      <el-divider />
      <el-form label-position="top">
        <el-form-item label="PEM 证书链">
          <el-input v-model="cert.certificate" type="textarea" :rows="5" />
        </el-form-item>
        <el-form-item label="PEM 私钥">
          <el-input v-model="cert.privateKey" type="textarea" :rows="5" />
        </el-form-item>
      </el-form>
      <p class="form-help">HTTP-01 需要公网 TCP 80 可达；失败保留原证书。关闭 HTTPS 暂停续期。</p>
    </template>
    <el-form v-if="mode === 'username'" label-width="120px">
      <el-form-item label="新用户名"><el-input v-model="username.username" /></el-form-item>
      <el-form-item label="当前密码">
        <el-input v-model="username.password" type="password" show-password />
      </el-form-item>
      <el-form-item v-if="panel.auth.twoFactorEnabled" label="验证码">
        <el-input v-model="username.code" maxlength="6" />
      </el-form-item>
    </el-form>
    <el-form v-if="mode === 'password'" label-width="120px">
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
    <el-form v-if="mode === 'totp'" label-width="120px">
      <el-form-item label="当前密码">
        <el-input v-model="twoFactor.password" type="password" show-password />
      </el-form-item>
      <el-form-item v-if="secret" label="验证器密钥">
        <el-input :model-value="secret" readonly />
      </el-form-item>
      <el-form-item v-if="secret || panel.auth.twoFactorEnabled" label="验证码">
        <el-input v-model="twoFactor.code" maxlength="6" />
      </el-form-item>
      <p class="form-help">确认后全部旧会话失效；请先在验证器保存密钥。</p>
    </el-form>
    <template #footer>
      <el-button @click="mode = ''">取消</el-button>
      <el-button
        v-if="mode === 'runtime'"
        type="primary"
        :disabled="!!runtimeError"
        @click="changeRuntime"
      >
        保存并重新连接
      </el-button>
      <el-button v-if="mode === 'certificate'" type="primary" @click="certificate">
        验证并应用上传证书
      </el-button>
      <el-button v-if="mode === 'username'" type="primary" @click="changeUsername">
        修改用户名并退出
      </el-button>
      <el-button v-if="mode === 'password'" type="primary" @click="changePassword">
        修改密码并退出
      </el-button>
      <template v-if="mode === 'totp'">
        <el-button
          v-if="!secret && !panel.auth.twoFactorEnabled"
          type="primary"
          @click="totp('prepare')"
        >
          开始绑定
        </el-button>
        <el-button v-if="secret" type="primary" @click="totp('confirm')">确认开启</el-button>
        <el-button v-if="panel.auth.twoFactorEnabled" type="danger" @click="totp('disable')">
          关闭双因素认证
        </el-button>
      </template>
    </template>
  </AppDialog>
  <SecurityCredentials v-model="securityMode" />
</template>
