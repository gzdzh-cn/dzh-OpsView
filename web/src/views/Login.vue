<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePanel } from '../stores/panel'
import { post, errorText, setCSRF } from '../lib/api'
defineProps<{ connectionError: string }>()
const emit = defineEmits(['ready'])
const panel = usePanel(),
  username = ref(''),
  password = ref(''),
  token = ref(''),
  code = ref(''),
  busy = ref(false),
  error = ref('')
const setup = computed(() => panel.auth.setupRequired)
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
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <div class="login-page">
    <div class="login-story">
      <a class="brand" href="/">
        <span class="brand-symbol">O</span>
        <span>
          OpsView
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
      <form @submit.prevent="submit">
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
        <label>
          用户名
          <el-input v-model="username" autocomplete="username" required />
        </label>
        <label>
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
        <label v-if="!setup">
          双因素验证码
          <span class="muted">已开启时填写</span>
          <el-input
            v-model="code"
            inputmode="numeric"
            maxlength="6"
            placeholder="6 位验证码"
            autocomplete="one-time-code"
          />
        </label>
        <el-button type="primary" native-type="submit" :loading="busy" size="large">
          {{ setup ? '创建账号并进入' : '进入控制台' }}
          <span>→</span>
        </el-button>
      </form>
      <div class="login-help">
        忘记密码？在服务器执行
        <code>opsview reset-admin</code>
      </div>
    </section>
  </div>
</template>
