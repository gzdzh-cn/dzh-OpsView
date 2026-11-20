<script setup lang="ts">
import { ElMessageBox } from 'element-plus'
import { computed, ref, onMounted } from 'vue'
import { usePanel } from '../stores/panel'
import { api, post, errorText, setCSRF, APIError } from '../lib/api'
import { passkeyUnavailableReason } from '../lib/browser'
import { loginPasskey } from '../lib/passkeys'
import { appearance } from '../lib/appearance'
defineProps<{ connectionError: string }>()
const emit = defineEmits(['ready'])
const panel = usePanel(),
  username = ref(''),
  password = ref(''),
  token = ref(''),
  code = ref(''),
  needsTOTP = ref(false),
  busy = ref(false),
  error = ref('')
const setup = computed(() => panel.auth.setupRequired)
const temporary = ref(false),
  temporaryToken = ref(''),
  passkeyAvailable = ref(false)
onMounted(() => {
  void api('/public/preferences')
    .then((p) => {
      passkeyAvailable.value = !!p.passkeyAvailable && !passkeyUnavailableReason()
    })
    .catch(() => {})
})
async function usePasskey() {
  busy.value = true
  error.value = ''
  try {
    let passkeyCode = ''
    if (panel.auth.twoFactorEnabled) {
      const result = await ElMessageBox.prompt('请输入验证器中的六位验证码', '验证登录', {
        inputPattern: /^\d{6}$/,
        inputErrorMessage: '请输入六位数字',
        confirmButtonText: '继续',
        cancelButtonText: '取消',
      })
      passkeyCode = result.value
    }
    const data = await loginPasskey(passkeyCode)
    setCSRF(data.csrf)
    await panel.session()
    emit('ready')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function exchange() {
  busy.value = true
  error.value = ''
  try {
    await post('/auth/temporary', { token: temporaryToken.value })
    temporaryToken.value = ''
    await panel.session()
    emit('ready')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function submit() {
  busy.value = true
  error.value = ''
  try {
    if (setup.value) {
      await post('/auth/setup', {
        username: username.value,
        password: password.value,
        token: token.value,
      })
      await panel.session()
    }
    const data = await post('/auth/login', {
      username: username.value,
      password: password.value,
      code: code.value,
    })
    setCSRF(data.csrf)
    await panel.session()
    if (!panel.auth.authenticated) {
      throw new Error('浏览器未保存登录会话，请允许此站点使用 Cookie，或使用无痕窗口重试。')
    }
    emit('ready')
  } catch (e) {
    if (e instanceof APIError && e.code === 'totp_required') {
      needsTOTP.value = true
      code.value = ''
    } else error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
function toggleTemporary() {
  resetLogin()
  temporary.value = !temporary.value
  temporaryToken.value = ''
}
function resetLogin() {
  needsTOTP.value = false
  code.value = ''
  password.value = ''
  error.value = ''
}
</script>
<template>
  <div
    class="login-page"
    :style="
      appearance.loginBackground
        ? { backgroundImage: `url(${appearance.loginBackground})`, backgroundSize: 'cover' }
        : {}
    "
  >
    <div class="login-story">
      <a class="brand" href="/">
        <img
          v-if="appearance.loginLogo"
          :src="appearance.loginLogo"
          class="brand-symbol"
          alt="登录 Logo"
        />
        <span v-else class="brand-symbol">O</span>
        <span>
          {{ appearance.name || 'OpsView' }}
          <span class="brand-dot">.</span>
          <small>SERVER CONTROL PANEL</small>
        </span>
      </a>
      <div>
        <div class="eyebrow">LESS FRICTION. MORE BUILDING.</div>
        <h1>
          你的服务器，
          <br />
          井然有序。
        </h1>
        <p>
          从一个站点到整个运行环境，
          <br />
          用更清晰的方式，管理每一次部署。
        </p>
        <div class="login-terminal">
          <div>
            <i></i>
            <i></i>
            <i></i>
            <span>opsview — workspace</span>
          </div>
          <pre><span>$</span> everything under control<br/><b>✓</b> Websites & reverse proxies<br/><b>✓</b> MySQL & PostgreSQL<br/><b>✓</b> Containers & system services<br/><b>✓</b> Backups & security</pre>
        </div>
      </div>
      <small>轻量 · 独立 · 可扩展</small>
    </div>
    <section class="login-form">
      <span class="login-overline">欢迎使用 OPSVIEW</span>
      <h2>{{ setup ? '创建你的管理员账号' : '登录工作空间' }}</h2>
      <p>{{ setup ? '使用本机初始化令牌，安全地开始。' : '管理服务器，从这里开始。' }}</p>
      <el-alert
        v-if="error || connectionError"
        :title="error || connectionError"
        type="error"
        :closable="false"
        show-icon
      />
      <form v-if="!temporary" @submit.prevent="submit">
        <label v-if="setup">
          初始化令牌
          <el-input
            v-model="token"
            type="password"
            autocomplete="off"
            placeholder="启动输出或 setup.token 中的令牌"
            show-password
            required
          />
        </label>
        <label v-if="!needsTOTP">
          用户名
          <el-input v-model="username" autocomplete="username" required />
        </label>
        <label v-if="!needsTOTP">
          密码
          <el-input
            v-model="password"
            type="password"
            :autocomplete="setup ? 'new-password' : 'current-password'"
            :placeholder="setup ? '至少 12 位，最多 72 字节' : '输入管理员密码'"
            show-password
            required
          />
        </label>
        <label v-if="needsTOTP">
          验证码
          <span class="muted">请打开验证器完成验证</span>
          <el-input
            v-model="code"
            aria-label="验证码"
            inputmode="numeric"
            maxlength="6"
            placeholder="6 位验证码"
            autocomplete="one-time-code"
            required
            autofocus
          />
        </label>
        <el-button type="primary" native-type="submit" :loading="busy" size="large">
          {{ setup ? '创建账号并进入' : needsTOTP ? '验证并登录' : '进入控制台' }}
          <span aria-hidden="true">→</span>
        </el-button>
        <el-button v-if="needsTOTP" link :disabled="busy" @click="resetLogin">
          返回账号登录
        </el-button>
      </form>
      <el-button
        v-if="!setup && passkeyAvailable && !temporary && !needsTOTP"
        :loading="busy"
        @click="usePasskey"
      >
        使用 Passkey 登录
      </el-button>
      <form v-if="temporary" @submit.prevent="exchange">
        <label>
          临时授权令牌
          <el-input v-model="temporaryToken" type="password" show-password autocomplete="off" />
        </label>
        <el-button type="primary" native-type="submit" :loading="busy">兑换只读访问</el-button>
      </form>
      <el-button v-if="!setup" link @click="toggleTemporary">
        {{ temporary ? '返回管理员登录' : '临时授权访问' }}
      </el-button>
      <div class="login-help">
        忘记密码？在服务器执行
        <code>opsview reset-admin</code>
      </div>
    </section>
  </div>
</template>
