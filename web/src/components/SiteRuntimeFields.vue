<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { api, errorText, type Config, type Resource } from '../lib/api'
const model = defineModel<Config>({ required: true })
const props = defineProps<{ site?: Resource }>()
const users = ref<Config[]>([]),
  error = ref(''),
  loading = ref(true),
  preview = ref<Config>(),
  checking = ref(false)
const password = ref(''),
  code = ref(''),
  confirmed = ref(false)
const controller = new AbortController()
onBeforeUnmount(() => {
  controller.abort()
  password.value = ''
  code.value = ''
})
api('/sites/runtime-users', { signal: controller.signal })
  .then((v) => {
    users.value = v.users || []
  })
  .catch((e) => {
    if (!controller.signal.aborted) error.value = errorText(e)
  })
  .finally(() => (loading.value = false))
const approvalKeys = [
  'user',
  'executable',
  'arguments',
  'environment',
  'rootDirectory',
  'runtimeIsolation',
]
const needsApproval = computed(
  () =>
    model.value.user === 'root' &&
    (!props.site || approvalKeys.some((k) => model.value[k] !== props.site?.config[k])),
)
const userInfo = computed(() => users.value.find((v) => v.name === model.value.user))
watch(
  () => [model.value.rootDirectory, model.value.user, model.value.executable],
  () => {
    clearCredentials()
    preview.value = undefined
    delete model.value.permissionPreview
  },
)
async function inspect() {
  if (checking.value) return
  checking.value = true
  error.value = ''
  try {
    const config = { ...model.value, runtimeIsolation: true }
    preview.value = await api('/sites/runtime-preview', {
      method: 'POST',
      signal: controller.signal,
      body: JSON.stringify({ id: props.site?.id, revision: props.site?.revision, config }),
    })
  } catch (e) {
    if (!controller.signal.aborted) error.value = errorText(e)
  } finally {
    checking.value = false
  }
}
function acceptPreview(value: boolean | string | number) {
  if (value && preview.value) {
    model.value.permissionPreview = preview.value.fingerprint
    model.value.runtimeIsolation = true
  } else delete model.value.permissionPreview
}
function headers(): Record<string, string> {
  if (!needsApproval.value) return {}
  if (!confirmed.value || !password.value) throw new Error('请选择确认并填写当前管理员密码')
  return { 'X-OpsView-Password': password.value, 'X-OpsView-TOTP': code.value }
}
function clearCredentials() {
  password.value = ''
  code.value = ''
  confirmed.value = false
}
defineExpose({ headers, clearCredentials })
</script>
<template>
  <el-form-item label="运行端口" required>
    <div class="runtime-port-row">
      <el-input-number v-model="model.port" :min="1024" :max="65535" />
      <el-checkbox v-model="model.releasePort">放行端口</el-checkbox>
    </div>
    <small class="form-help">仅处理本机 TCP 防火墙；不改变程序监听地址，云安全组需另行配置。</small>
  </el-form-item>
  <el-form-item label="运行用户">
    <el-select v-model="model.user" filterable :loading="loading" aria-label="运行用户">
      <el-option label="独立站点用户（自动创建）" value="isolated" />
      <el-option
        v-for="u in users"
        :key="u.name"
        :label="u.name + ' · ' + u.group"
        :value="u.name"
      />
      <el-option
        v-if="model.user && model.user !== 'isolated' && !userInfo"
        :label="model.user + '（账号待核对）'"
        :value="model.user"
        disabled
      />
    </el-select>
    <small v-if="model.user === 'isolated' || !model.user" class="form-help">
      独立用户 · 面板创建专属用户与组，禁止交互登录；文件保留或在回收站时保留账号。
    </small>
    <small v-else-if="userInfo" class="form-help">
      共享/已有账号 · UID {{ userInfo.uid }} · 组 {{ userInfo.group }}（{{
        userInfo.gid
      }}），不随站点删除。
    </small>
  </el-form-item>
  <el-form-item label="运行限制">
    <div>
      <el-tag :type="model.runtimeIsolation ? 'success' : 'info'">
        {{
          model.runtimeIsolationApplied
            ? '运行限制已启用'
            : model.runtimeIsolation
              ? '待应用运行限制'
              : '旧项目尚未整改'
        }}
      </el-tag>
      <p class="form-help">
        仅开放本项目及必要运行依赖，屏蔽其他站点和面板私有目录。共享账号与受限 root
        不代表完整主机隔离。
      </p>
      <el-button :loading="checking" @click="inspect">检查与整改</el-button>
      <p v-if="preview" class="form-help">
        {{ preview.path }} · {{ preview.entries }} 项。{{ preview.changes }}
      </p>
      <el-checkbox v-if="preview" :model-value="!!model.permissionPreview" @change="acceptPreview">
        确认整理以上目录权限并启用运行限制
      </el-checkbox>
      <p v-if="model.adoptDirectory && !model.permissionPreview" class="form-help">
        纳管已有目录前，请先检查并确认权限调整范围。
      </p>
    </div>
  </el-form-item>
  <template v-if="needsApproval">
    <el-form-item label="root 确认">
      <el-checkbox v-model="confirmed">确认以 root 运行，并保留目录及 systemd 限制</el-checkbox>
    </el-form-item>
    <el-form-item label="管理员密码">
      <el-input v-model="password" type="password" autocomplete="off" show-password />
    </el-form-item>
    <el-form-item label="双因素验证码">
      <el-input
        v-model="code"
        maxlength="6"
        placeholder="仅已启用双因素时填写"
        autocomplete="one-time-code"
      />
    </el-form-item>
  </template>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
</template>
<style scoped>
.runtime-port-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
</style>
