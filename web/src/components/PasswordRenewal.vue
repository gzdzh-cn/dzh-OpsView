<script setup lang="ts">
import { ref } from 'vue'
import { usePanel } from '../stores/panel'
import { post, errorText } from '../lib/api'
const panel = usePanel(),
  current = ref(''),
  password = ref(''),
  code = ref(''),
  busy = ref(false),
  error = ref('')
async function save() {
  busy.value = true
  try {
    await post('/auth/password', {
      current: current.value,
      password: password.value,
      code: code.value,
    })
    current.value = ''
    password.value = ''
    await panel.session()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <main class="initial-loading">
    <section class="surface" style="padding: 24px; max-width: 480px; width: 100%">
      <p>管理员密码已到期，请修改后重新登录。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <el-form label-position="top" @submit.prevent="save">
        <el-form-item label="当前密码">
          <el-input
            v-model="current"
            type="password"
            show-password
            autocomplete="current-password"
          />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="password" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <el-form-item v-if="panel.auth.twoFactorEnabled" label="双因素验证码">
          <el-input v-model="code" maxlength="6" />
        </el-form-item>
        <el-button type="primary" native-type="submit" :loading="busy">修改密码</el-button>
      </el-form>
    </section>
  </main>
</template>
