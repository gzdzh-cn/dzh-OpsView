<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, post, errorText, type Config } from '../lib/api'
import { identity } from '../lib/identity'
import { registerPasskey } from '../lib/passkeys'
import AppDialog from './AppDialog.vue'
import DataList from './DataList.vue'
import LoadingContent from './LoadingContent.vue'
const supportsPasskeys = !!window.PublicKeyCredential
function copyToken() {
  void navigator.clipboard.writeText(token.value)
}
const mode = defineModel<string>({ default: '' }),
  rows = ref<Config[]>([]),
  busy = ref(false),
  error = ref(''),
  reason = ref(''),
  token = ref(''),
  cert = ref<Config>(),
  createOpen = ref(false),
  name = ref(''),
  days = ref(30),
  sources = ref(''),
  scopes = ref(['overview:read', 'resources:read']),
  ca = ref('')
import { watch } from 'vue'
let sequence = 0
async function load() {
  const seq = ++sequence
  busy.value = true
  error.value = ''
  try {
    const path =
      mode.value === 'sessions'
        ? '/auth/sessions'
        : mode.value === 'passkeys'
          ? '/auth/passkeys/list'
          : '/auth/tokens/' + mode.value
    const data = await api(path)
    if (seq !== sequence) return
    rows.value = mode.value === 'passkeys' ? data.rows : data
    reason.value = data.reason || ''
  } catch (e) {
    if (seq === sequence) error.value = errorText(e)
  } finally {
    if (seq === sequence) busy.value = false
  }
}
watch(mode, (v) => {
  sequence++
  createOpen.value = false
  scopes.value = ['overview:read', 'resources:read']
  token.value = ''
  cert.value = undefined
  rows.value = []
  if (v && !['certificate'].includes(v)) void load()
  else busy.value = false
})
onBeforeUnmount(() => {
  sequence++
  token.value = ''
  cert.value = undefined
})
async function revoke(row?: Config) {
  try {
    await ElMessageBox.confirm(row ? '撤销此授权？' : '使所有登录会话失效？', '撤销访问', {
      type: 'warning',
      confirmButtonText: '撤销',
      cancelButtonText: '取消',
    })
    const auth = await identity()
    await post(
      mode.value === 'sessions'
        ? '/auth/sessions'
        : mode.value === 'passkeys'
          ? '/auth/passkeys/delete'
          : '/auth/tokens/' + mode.value,
      { ...auth, id: row?.id, all: !row },
    )
    await load()
    ElMessage.success('已撤销')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function register() {
  busy.value = true
  try {
    await registerPasskey(await identity())
    await load()
    ElMessage.success('Passkey 已绑定')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function create() {
  busy.value = true
  try {
    const data = await post('/auth/tokens/' + mode.value, {
      ...(await identity()),
      name: name.value,
      days: days.value,
      sources: sources.value,
      scopes: scopes.value,
    })
    token.value = data.token
    createOpen.value = false
    await load()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
async function generate() {
  busy.value = true
  try {
    cert.value = await post('/auth/client-certificate', await identity())
    ca.value = cert.value?.ca || ''
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
function download(value: string, filename: string) {
  const url = URL.createObjectURL(new Blob([value], { type: 'application/x-pem-file' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
</script>
<template>
  <AppDialog
    :model-value="!!mode"
    :title="
      (
        {
          sessions: '登录会话',
          passkeys: 'Passkey',
          api: 'API 凭据',
          temporary: '临时访问授权',
          certificate: '客户端证书',
        } as Record<string, string>
      )[mode]
    "
    width="880px"
    @update:model-value="mode = ''"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div class="dialog-stack">
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <template v-if="mode === 'certificate'">
        <el-alert
          title="客户端私钥仅本次提供。下载后妥善保存，浏览器需导入个人证书后再开启认证。可用 openssl pkcs12 -export 将证书及私钥转换为 PKCS#12。"
          type="warning"
          :closable="false"
        />
        <el-button :loading="busy" @click="generate">生成客户端证书</el-button>
        <template v-if="cert">
          <el-button @click="download(cert.certificate, 'opsview-client.crt')">下载证书</el-button>
          <el-button @click="download(cert.privateKey, 'opsview-client.key')">
            下载私钥（仅本次）
          </el-button>
          <el-button @click="download(ca, 'opsview-client-ca.crt')">下载 CA</el-button>
        </template>
      </template>
      <template v-else>
        <div class="page-toolbar">
          <template v-if="mode === 'sessions'">
            <el-button type="danger" plain @click="revoke()">全部退出</el-button>
          </template>
          <el-button
            v-else-if="mode === 'passkeys'"
            type="primary"
            :disabled="!!reason || !supportsPasskeys"
            @click="register"
          >
            绑定 Passkey
          </el-button>
          <el-button
            v-else
            type="primary"
            @click="
              createOpen = true; token = ''
            "
          >
            创建{{ mode === 'api' ? '凭据' : '临时授权' }}
          </el-button>
        </div>
        <el-alert v-if="reason" :title="reason" type="info" :closable="false" />
        <div v-if="token" class="notice warn">
          <p>
            仅显示一次，请立即复制。{{
              mode === 'temporary' ? '十分钟有效，一次兑换；在登录页的临时授权入口输入。' : ''
            }}
          </p>
          <el-input :model-value="token" readonly aria-label="新授权令牌" />
          <el-button @click="copyToken">复制</el-button>
        </div>
        <LoadingContent :busy="busy">
          <DataList :rows="rows" :state-key="'security-' + mode">
            <el-table-column v-if="mode === 'sessions'" label="来源" min-width="170">
              <template #default="{ row }">
                {{ row.ip }} {{ row.current ? '（当前）' : '' }}
                <small class="cell-subtext">{{ row.agent }}</small>
              </template>
            </el-table-column>
            <el-table-column
              v-else
              :prop="mode === 'passkeys' ? 'id' : 'name'"
              label="名称 / 标识"
              min-width="180"
              show-overflow-tooltip
            />
            <el-table-column
              v-if="mode === 'api' || mode === 'temporary'"
              label="范围"
              min-width="180"
            >
              <template #default="{ row }">{{ row.scopes?.join('、') }}</template>
            </el-table-column>
            <el-table-column v-if="mode !== 'passkeys'" label="到期时间" min-width="170">
              <template #default="{ row }">
                {{ new Date(row.expires * 1000).toLocaleString() }}
              </template>
            </el-table-column>
            <el-table-column v-if="mode === 'api'" label="最近使用" min-width="170">
              <template #default="{ row }">
                {{ row.lastUsed ? new Date(row.lastUsed * 1000).toLocaleString() : '未使用' }}
              </template>
            </el-table-column>
            <el-table-column label="操作" width="90">
              <template #default="{ row }">
                <el-button type="danger" link @click="revoke(row)">撤销</el-button>
              </template>
            </el-table-column>
          </DataList>
        </LoadingContent>
        <p v-if="mode === 'api'" class="form-help">
          API 默认关闭；开启后仅接受 HTTPS。使用 X-OpsView-API-Key
          请求头传递令牌，不能修改安全设置或执行脚本。
        </p>
      </template>
    </div>
    <template #footer><el-button @click="mode = ''">关闭</el-button></template>
  </AppDialog>
  <AppDialog
    v-model="createOpen"
    :title="mode === 'api' ? '创建 API 凭据' : '创建临时授权'"
    width="650px"
    size="small"
  >
    <el-form label-width="110px">
      <el-form-item label="名称"><el-input v-model="name" maxlength="80" /></el-form-item>
      <el-form-item label="授权范围">
        <el-checkbox-group v-model="scopes">
          <el-checkbox value="overview:read">概览只读</el-checkbox>
          <el-checkbox value="resources:read">资源只读</el-checkbox>
          <el-checkbox value="tasks:read">任务只读</el-checkbox>
          <template v-if="mode === 'api'">
            <el-checkbox value="sites:write">网站启停/续期</el-checkbox>
            <el-checkbox value="backups:write">执行备份</el-checkbox>
          </template>
        </el-checkbox-group>
      </el-form-item>
      <el-form-item v-if="mode === 'api'" label="有效期（天）">
        <el-input-number v-model="days" :min="1" :max="365" />
      </el-form-item>
      <el-form-item label="来源 IP/CIDR">
        <el-input
          v-model="sources"
          type="textarea"
          :rows="3"
          placeholder="留空不限来源，空格或换行分隔"
        />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="createOpen = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="create">验证并创建</el-button>
    </template>
  </AppDialog>
</template>
