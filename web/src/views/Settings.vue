<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import SettingRow from '../components/SettingRow.vue'
import SecurityCredentials from '../components/SecurityCredentials.vue'
import { identity } from '../lib/identity'
import { applyAppearance } from '../lib/appearance'
const NotificationManager = defineAsyncComponent(
  () => import('../components/NotificationManager.vue'),
)
const BackupRecords = defineAsyncComponent(() => import('../components/BackupRecords.vue'))
import {
  computed,
  defineAsyncComponent,
  onMounted,
  onBeforeUnmount,
  nextTick,
  toRaw,
  ref,
  watch,
} from 'vue'
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
  runtimeBusy = ref(false),
  runtimeLoading = ref(false),
  savedRuntime = ref<Config>({}),
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
  {
    key: 'backupStorageId',
    label: '面板备份存储',
    group: 'backup',
    help: '保留本地加密副本，可同时上传已有 S3 存储',
    type: 'storage',
  },
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
    key: 'backupRetention',
    label: '面板备份保留数量',
    group: 'backup',
    help: '新备份成功后才清理旧备份，主密钥独立保管',
    type: 'number',
    min: 1,
    max: 30,
  },
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
      ? ['refreshSeconds', 'siteDirectory', 'backupDirectory', 'sessionMinutes'].includes(f.key)
      : tab.value === 'panel'
        ? f.group === 'panel' && f.key !== 'refreshSeconds'
        : f.group === tab.value,
  ),
)
function valueLabel(f: Field) {
  const v = settings.value[f.key]
  if (f.type === 'switch' || f.type === 'basic') return v ? '已开启' : '已关闭'
  if (f.type === 'storage')
    return v ? panel.resources.find((r) => r.id === v)?.name || '存储已移除' : '仅本地'
  if (f.type === 'image') return v ? '已上传' : '默认'
  if (Array.isArray(v)) return v.length ? v.join('、') : '默认'
  return f.options?.find((o) => o.value === v)?.label ?? v ?? '—'
}
let editSequence = 0
let editRead: AbortController | undefined
let fieldLoading = false
const fieldReady = ref(false)
async function edit(f: Field) {
  if (busy.value || settingsLoading.value) return
  cancelEdits()
  const seq = ++editSequence
  field.value = f
  editValue.value = structuredClone(toRaw(settings.value[f.key]))
  editor.value = true
  fieldReady.value = false
  busy.value = true
  fieldLoading = true
  editRead = new AbortController()
  try {
    const result = await api('/settings/groups/' + f.group, { signal: editRead.signal })
    if (seq !== editSequence) return
    Object.assign(settings.value, result.config)
    groupRevisions.value[f.group] = result.revision
    editValue.value = structuredClone(toRaw(settings.value[f.key]))
    basicUsername.value = settings.value.basicUsername || ''
    countriesText.value = (settings.value.loginCountries || []).join(' ')
    unknownCountry.value = settings.value.unknownCountry || 'deny'
    fieldReady.value = true
    if (f.type === 'countries') void regionStatus()
  } catch (e) {
    if (seq === editSequence) editError.value = errorText(e)
  } finally {
    if (seq === editSequence) {
      fieldLoading = false
      busy.value = false
      await nextTick()
      document
        .getElementById('setting-' + f.key)
        ?.querySelector('input')
        ?.focus()
    }
  }
}
function cancelEdits() {
  editSequence++
  editRead?.abort()
  if (fieldLoading) {
    busy.value = false
    fieldLoading = false
  }
  editor.value = false
  fieldReady.value = false
  basicPassword.value = ''
  mode.value = ''
  securityMode.value = ''
  editError.value = ''
  clearModeSecrets()
}
async function save() {
  if (!field.value || !fieldReady.value || busy.value) return
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
    cancelEdits()
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
function clearModeSecrets() {
  password.value = { current: '', password: '', code: '' }
  username.value = { username: '', password: '', code: '' }
  twoFactor.value = { password: '', code: '' }
  secret.value = ''
  cert.value = { certificate: '', privateKey: '' }
}
function openMode(value: string) {
  if (
    busy.value ||
    runtimeBusy.value ||
    (runtimeLoading.value && ['entry', 'runtime'].includes(value))
  )
    return
  cancelEdits()
  if (['entry', 'runtime'].includes(value))
    runtime.value = structuredClone(toRaw(savedRuntime.value))
  mode.value = value
  void nextTick(() =>
    document
      .querySelector<HTMLInputElement>(
        '.settings-surface .preference-item:has(.settings-inline-details) input',
      )
      ?.focus(),
  )
}
function openSecurity(value: string) {
  if (busy.value) return
  cancelEdits()
  securityMode.value = value
}
const extraRows = computed(() => {
  const rows: {
    label: string
    value: string
    help?: string
    mode?: string
    security?: string
    link?: string
  }[] = []
  if (tab.value === 'common')
    rows.push({
      label: '管理员',
      value: panel.auth.username || '—',
      help: '修改后旧会话失效',
      mode: 'username',
    })
  if (tab.value === 'panel')
    rows.push(
      {
        label: '面板访问',
        value: runtime.value.protocol + ' · ' + runtime.value.listenHost + ':' + runtime.value.port,
        help: runtimeError.value || '公网地址、IPv4/IPv6、端口及协议；120 秒连接确认',
        mode: 'runtime',
      },
      {
        label: '安全入口',
        value: runtime.value.entry || '未开启',
        help: '8–64 位字母、数字、横线或下划线；留空关闭',
        mode: 'entry',
      },
      {
        label: '面板证书',
        value: certificateStatus.value.expires
          ? new Date(certificateStatus.value.expires).toLocaleString()
          : '—',
        help: certificateStatus.value.issuer || '申请、上传及续期',
        mode: 'certificate',
      },
      { label: '时间同步', value: '系统时间服务', help: '通过受控计划任务同步', link: '/backups' },
    )
  if (tab.value === 'security')
    rows.push(
      {
        label: '安全入口及来源',
        value: runtime.value.entry || '未设置安全入口',
        help: '安全入口、访问域名及 IP/CIDR 限制',
        mode: 'runtime',
      },
      { label: '管理员用户名', value: panel.auth.username || '—', mode: 'username' },
      { label: '管理员密码', value: '已设置', mode: 'password' },
      {
        label: '双因素认证',
        value: panel.auth.twoFactorEnabled ? '已开启' : '未开启',
        mode: 'totp',
      },
      { label: '登录会话', value: '管理在线会话', security: 'sessions' },
      {
        label: 'Passkey',
        value: '管理通行密钥',
        help: '有效 HTTPS 域名、安全上下文及设备验证',
        security: 'passkeys',
      },
      {
        label: '客户端证书',
        value: '生成并下载',
        help: '私钥仅在生成时提供；请先导入再启用认证',
        security: 'certificate',
      },
      { label: 'API 凭据', value: '管理范围、来源及到期', security: 'api' },
      { label: '临时访问授权', value: '只读 · 十分钟 · 一次兑换', security: 'temporary' },
    )
  return rows
})
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
let runtimeSequence = 0
async function loadRuntime() {
  const seq = ++runtimeSequence
  runtimeLoading.value = true
  try {
    const result = await api('/settings/runtime', { signal: reads.signal })
    if (seq !== runtimeSequence) return
    runtime.value = {
      ...runtime.value,
      ...result.config,
      allowedCIDRs: result.config.allowedCIDRs ?? settings.value.allowedCIDRs ?? '',
    }
    savedRuntime.value = structuredClone(toRaw(runtime.value))
    certificateStatus.value = result.certificate || {}
    acme.value.host = runtime.value.publicHost
    runtimeError.value = ''
  } catch (e) {
    if (!reads.signal.aborted && seq === runtimeSequence) runtimeError.value = errorText(e)
  } finally {
    if (seq === runtimeSequence) runtimeLoading.value = false
  }
}
async function changeRuntime() {
  if (runtimeBusy.value || runtimeError.value) return
  runtimeBusy.value = true
  editError.value = ''
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
    savedRuntime.value = structuredClone(toRaw(runtime.value))
    mode.value = ''
    ElMessage.success('设置已暂存，请通过下面的新地址重新连接')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') editError.value = errorText(e)
  } finally {
    runtimeBusy.value = false
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
if (tab.value === 'appearance') tab.value = 'common'
onBeforeUnmount(cancelEdits)
watch(tab, (value) => {
  cancelEdits()
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
      :id="'setting-' + f.key"
      :key="f.key"
      :label="f.label"
      :value="valueLabel(f)"
      :help="f.help"
      :editing="editor && field?.key === f.key"
      :busy="busy"
      :disabled="settingsLoading || (editor && field?.key === f.key && !fieldReady)"
      @edit="edit(f)"
      @save="save"
      @cancel="cancelEdits"
    >
      <template #editor>
        <el-switch v-if="f.type === 'switch'" v-model="editValue" :disabled="busy || !fieldReady" />
        <el-input-number
          v-else-if="f.type === 'number'"
          v-model="editValue"
          :min="f.min"
          :max="f.max"
          :disabled="busy || !fieldReady"
          @keydown.enter.prevent="save"
          @keydown.esc="cancelEdits"
        />
        <el-select
          v-else-if="f.type === 'select' || f.type === 'storage'"
          v-model="editValue"
          :disabled="busy || !fieldReady"
        >
          <template v-if="f.type === 'storage'">
            <el-option label="仅本地" value="" />
            <el-option
              v-for="r in panel.resources.filter(
                (r) => r.kind === 'backup' && r.config.destination === 's3',
              )"
              :key="r.id"
              :value="r.id"
              :label="r.name"
            />
          </template>
          <el-option
            v-else
            v-for="o in f.options"
            :key="o.value"
            :value="o.value"
            :label="o.label"
          />
        </el-select>
        <span v-else-if="['basic', 'countries'].includes(f.type || '')">{{ valueLabel(f) }}</span>
        <el-input
          v-else
          v-model="editValue"
          :aria-label="f.label"
          :disabled="busy || !fieldReady"
          @keydown.enter.prevent="save"
          @keydown.esc="cancelEdits"
        />
      </template>
      <template #details>
        <div v-if="editor && field?.key === f.key" class="settings-inline-status">
          <el-alert v-if="editError" :title="editError" type="error" :closable="false">
            <el-button link :disabled="busy" @click="edit(f)">重新载入（放弃草稿）</el-button>
          </el-alert>
          <div v-if="['basic', 'countries'].includes(f.type || '')" class="settings-inline-details">
            <el-form
              v-if="field"
              :disabled="busy || !fieldReady"
              label-width="120px"
              @submit.prevent="save"
              @keydown.esc="cancelEdits"
            >
              <el-form-item :label="field.label">
                <el-switch
                  v-if="['switch', 'basic'].includes(field.type || '')"
                  v-model="editValue"
                />
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
          </div>
        </div>
      </template>
    </SettingRow>

    <SettingRow
      v-for="row in extraRows"
      :key="row.label"
      :label="row.label"
      :value="row.value"
      :help="row.help"
      :editing="row.mode === 'entry' && mode === 'entry'"
      :busy="runtimeBusy"
      :disabled="settingsLoading || runtimeLoading || (!!runtimeError && row.mode === 'entry')"
      @edit="row.mode ? openMode(row.mode) : row.security ? openSecurity(row.security) : undefined"
      @save="changeRuntime"
      @cancel="cancelEdits"
    >
      <template #editor>
        <el-input
          v-model="runtime.entry"
          aria-label="安全入口"
          :disabled="runtimeBusy"
          @keydown.enter.prevent="changeRuntime"
          @keydown.esc="cancelEdits"
        />
      </template>
      <template #action>
        <router-link v-if="row.link" :to="row.link">计划任务</router-link>
        <el-button
          v-else
          link
          type="primary"
          :disabled="
            settingsLoading ||
            busy ||
            runtimeBusy ||
            (runtimeLoading && ['entry', 'runtime'].includes(row.mode || ''))
          "
          @click="
            row.mode
              ? mode === row.mode
                ? cancelEdits()
                : openMode(row.mode)
              : row.security
                ? securityMode === row.security
                  ? cancelEdits()
                  : openSecurity(row.security)
                : undefined
          "
        >
          {{
            (row.mode && mode === row.mode) || (row.security && securityMode === row.security)
              ? '收起'
              : '修改'
          }}
        </el-button>
      </template>
      <template #details>
        <div
          v-if="row.mode && mode === row.mode"
          class="settings-inline-details"
          :class="{ 'settings-entry-details': mode === 'entry' }"
        >
          <el-alert v-if="editError" :title="editError" type="error" :closable="false" />
          <el-alert
            v-if="runtimeError && ['runtime', 'entry'].includes(mode)"
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
              <el-form-item label="访问域名">
                <el-input v-model="runtime.allowedHost" />
              </el-form-item>
              <el-form-item label="IP/CIDR 限制">
                <el-input v-model="runtime.allowedCIDRs" type="textarea" :rows="3" />
              </el-form-item>
            </el-form>
            <p class="form-help">
              域名变化可能使既有 Passkey 无法使用，请保留密码及本机恢复。HTTP 不支持
              Passkey、客户端证书及 API 凭据。
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
            <p class="form-help">
              HTTP-01 需要公网 TCP 80 可达；失败保留原证书。关闭 HTTPS 暂停续期。
            </p>
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

          <div
            v-if="mode !== 'entry'"
            class="settings-inline-actions"
            :aria-busy="runtimeBusy"
            :inert="runtimeBusy || undefined"
          >
            <el-button @click="mode = ''">取消</el-button>
            <el-button
              v-if="['runtime', 'entry'].includes(mode)"
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
          </div>
        </div>
        <SecurityCredentials
          v-if="row.security && securityMode === row.security"
          v-model="securityMode"
          inline
        />
      </template>
    </SettingRow>
    <div v-if="pendingURL" class="notice warn">
      <a :href="pendingURL" target="_blank" rel="noopener">打开新地址并确认（120 秒内）</a>
    </div>
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
</template>
