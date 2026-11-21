<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import SiteRuntimeFields from './SiteRuntimeFields.vue'
const runtimeFields = ref<InstanceType<typeof SiteRuntimeFields>>()
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { randomID, randomBytes } from '../lib/browser'
import {
  siteCreationRequest as requestId,
  setSiteCreationRequest,
  lookupSiteCreation,
} from '../lib/siteCreation'
import { observeSiteTask } from '../lib/siteTask'
import { isAbort } from '../lib/query'
import { ElMessage } from 'element-plus'
import { usePanel } from '../stores/panel'
import { api, post, errorText, type Config, type Resource } from '../lib/api'
const props = defineProps<{ modelValue: boolean; initial: Config; editing?: Resource }>()
const emit = defineEmits(['update:modelValue'])
const panel = usePanel()
const visible = computed({ get: () => props.modelValue, set: (v) => emit('update:modelValue', v) })
const form = ref<Config>({}),
  name = ref(''),
  busy = ref(false),
  error = ref(''),
  categories = ref<string[]>(['默认分类'])
function generatePassword() {
  try {
    database.value.password = Array.from(randomBytes(16), (v) =>
      v.toString(16).padStart(2, '0'),
    ).join('')
  } catch (e) {
    error.value = errorText(e)
  }
}
const database = ref<Config>({
  instanceId: '',
  name: '',
  username: '',
  password: '',
  encoding: '',
  collation: '',
})
const databaseInstances = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) =>
        ['mysql', 'postgresql'].includes(r.config.software) &&
        ['ready', 'running'].includes(r.status),
    ),
)
const certificate = ref(''),
  privateKey = ref(''),
  certificateMode = ref('none')
const siteDirectory = ref('/www/wwwroot')
let openSequence = 0
const saved = ref<Resource>()
const databaseReady = ref(false)
let observer: AbortController | undefined
onBeforeUnmount(() => {
  openSequence++
  observer?.abort()
  database.value.password = ''
  privateKey.value = ''
  form.value.environment = ''
})
async function finishCreation(taskId: string, signal: AbortSignal) {
  const task = await observeSiteTask(taskId, signal)
  signal.throwIfAborted()
  await panel.refresh(['resources', 'tasks'])
  signal.throwIfAborted()
  if (saved.value)
    saved.value = panel.resources.find((r) => r.id === saved.value?.id) || saved.value
  if (task.state !== 'succeeded') {
    const result = requestId.value ? await lookupSiteCreation(requestId.value, signal) : null
    databaseReady.value = !!result?.databaseReady
    throw new Error(task.error || '创建未完成，请检查错误后重新提交')
  }
  setSiteCreationRequest('')
  ElMessage.success('项目创建成功')
  visible.value = false
}
async function recoverCreation(signal: AbortSignal) {
  if (!requestId.value) return
  const progress = await lookupSiteCreation(requestId.value, signal)
  signal.throwIfAborted()
  if (!progress) return
  if (progress.state === 'succeeded') {
    setSiteCreationRequest('')
    return
  }
  if (!panel.resources.some((r) => r.id === progress.siteId)) await panel.refresh(['resources'])
  signal.throwIfAborted()
  const existing = panel.resources.find((r) => r.id === progress.siteId)
  if (!existing)
    throw new Error('创建记录存在，但站点信息暂时无法载入，请重新打开后核对，勿重复创建')
  if (existing) {
    saved.value = existing
    form.value = JSON.parse(JSON.stringify(existing.config))
    if (form.value.type === 'static') {
      form.value.type = 'php'
      form.value.phpInstanceId = 'static'
    }
    name.value = existing.name
    rootChanged.value = true
    databaseReady.value = !!progress.databaseReady
  }
  if (['queued', 'running'].includes(progress.state)) {
    busy.value = true
    try {
      await finishCreation(progress.taskId, signal)
    } catch (e) {
      if (!isAbort(e)) error.value = errorText(e)
    }
  } else error.value = '上次创建未完成，可修正尚未生效的配置后提交。'
}
const directoryOpen = ref(false),
  directories = ref<Config[]>([]),
  directoryError = ref(''),
  directoryPage = ref(1),
  directoryTotal = ref(0),
  directoryBusy = ref(false)
async function browseDirectory() {
  directoryOpen.value = true
  directoryError.value = ''
  directoryBusy.value = true
  try {
    const result = await post('/files', {
      scope: 'server',
      action: 'list',
      path: siteDirectory.value,
      page: directoryPage.value,
      pageSize: 100,
    })
    directories.value = result.items.filter((r: Config) => r.directory && !r.symlink)
    directoryTotal.value = result.total
  } catch (e) {
    directoryError.value = errorText(e)
  } finally {
    directoryBusy.value = false
  }
}
const nameChanged = ref(false),
  rootChanged = ref(false)
const instances = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) => r.config.software === 'nginx' && ['ready', 'running', 'stopped'].includes(r.status),
    ),
)
const php = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) =>
        r.config.software === 'php' &&
        r.config.mode === 'native' &&
        ['ready', 'running', 'stopped'].includes(r.status),
    ),
)
const title = computed(
  () =>
    (props.editing ? '编辑' : '添加') +
    (form.value.type === 'php'
      ? ' PHP 站点'
      : form.value.type === 'proxy'
        ? '反向代理'
        : '其他项目'),
)
const firstDomain = computed(
  () =>
    String(form.value.domains || '')
      .trim()
      .split(/\s+/)[0]
      ?.toLowerCase() || '',
)
watch(
  () => props.modelValue,
  async (open) => {
    const seq = ++openSequence
    if (!open) {
      observer?.abort()
      certificate.value = ''
      privateKey.value = ''
      form.value.environment = ''
      database.value.password = ''
      return
    }
    observer?.abort()
    observer = new AbortController()
    const signal = observer.signal
    form.value = JSON.parse(
      JSON.stringify(
        props.editing?.config || {
          type: 'php',
          domains: '',
          rootDirectory: siteDirectory.value,
          upstream: 'http://127.0.0.1:3000',
          proxyHost: '$http_host',
          note: '',
          securityHeaders: true,
          denyFrames: false,
          denyUploadPHP: true,
          autoStart: true,
          port: 3000,
          user: 'isolated',
          runtimeIsolation: true,
          releasePort: false,
          arguments: '[]',
          environment: '{}',
          ...props.initial,
        },
      ),
    )
    name.value = props.editing?.name || ''
    if (props.editing)
      form.value.rootDirectory =
        props.editing.config.documentRoot || props.editing.config.rootDirectory
    nameChanged.value = !!props.editing
    rootChanged.value = !!props.editing
    saved.value = undefined
    databaseReady.value = false
    error.value = ''
    busy.value = true
    database.value = {
      instanceId: '',
      name: '',
      username: '',
      password: '',
      encoding: '',
      collation: '',
    }
    form.value.category ||= '默认分类'
    categories.value = ['默认分类']
    certificateMode.value = 'none'
    if (form.value.type === 'php' && !form.value.phpInstanceId)
      form.value.phpInstanceId = php.value[0]?.id || 'static'
    try {
      const [defaults, groups] = await Promise.all([
        api('/settings', { signal }),
        api('/site-categories', { signal }),
      ])
      if (seq !== openSequence || !props.modelValue) return
      categories.value = groups.rows
      siteDirectory.value = defaults.siteDirectory || '/www/wwwroot'
      if (!props.editing && !rootChanged.value)
        form.value.rootDirectory =
          siteDirectory.value + (firstDomain.value ? '/' + firstDomain.value : '')
    } catch {
      /* server also applies the configured default */
    }
    try {
      if (seq === openSequence && props.modelValue) await recoverCreation(signal)
    } catch (e) {
      if (seq === openSequence && !isAbort(e)) error.value = errorText(e)
    } finally {
      if (seq === openSequence) busy.value = false
    }
  },
)
watch(firstDomain, (domain) => {
  if (props.editing || saved.value) return
  if (!nameChanged.value) name.value = domain.slice(0, 63)
  if (!database.value.name) {
    const n = 'site_' + domain.replace(/[^a-z0-9]/g, '_').slice(0, 40)
    if (domain) {
      database.value.name = n
      database.value.username = n.slice(0, 30)
    }
  }
  if (!rootChanged.value)
    form.value.rootDirectory =
      siteDirectory.value + (/^[a-z0-9][a-z0-9.-]*$/i.test(domain) ? '/' + domain : '')
})
async function save() {
  if (busy.value) return
  error.value = ''
  busy.value = true
  const signal = observer!.signal
  try {
    if (!name.value || !form.value.domains.trim()) throw new Error('请填写域名及项目名称')
    if (instances.value.length !== 1) throw new Error('请先安装唯一受管 Nginx，或清理历史多实例')
    if (
      form.value.type !== 'proxy' &&
      !props.editing &&
      form.value.rootDirectory === siteDirectory.value
    )
      throw new Error('请填写域名以生成项目文件夹')
    if (certificateMode.value === 'upload' && (!certificate.value || !privateKey.value))
      throw new Error('请填写证书链与私钥')
    const config: Config = {
      ...form.value,
      type:
        form.value.type === 'php' && form.value.phpInstanceId === 'static'
          ? 'static'
          : form.value.type,
      domains: form.value.domains.trim().split(/\s+/).join(' '),
    }
    // Deployed paths are executor-owned. rootDirectory is only a new-site request.
    if (props.editing) config.rootDirectory = props.editing.config.rootDirectory
    if (config.type === 'proxy') delete config.rootDirectory
    if (config.type === 'static') {
      delete config.phpInstanceId
      config.rewriteTemplate = 'none'
    }
    const headers = runtimeFields.value?.headers() || {}
    if (config.type === 'managed' && config.adoptDirectory && !config.permissionPreview)
      throw new Error('请先检查并确认目录权限调整')
    let result: Config
    if (saved.value) {
      const current = panel.resources.find((r) => r.id === saved.value?.id) || saved.value
      const progress = requestId.value ? await lookupSiteCreation(requestId.value, signal) : null
      signal.throwIfAborted()
      if (progress && ['queued', 'running', 'succeeded'].includes(progress.state)) result = progress
      else
        result = await api('/sites/' + saved.value.id + '/provision', {
          method: 'POST',
          headers,
          body: JSON.stringify({
            name: name.value,
            config,
            revision: current.revision,
          }),
        })
    } else {
      if (!requestId.value) setSiteCreationRequest(randomID())
      const existing = await lookupSiteCreation(requestId.value, signal)
      signal.throwIfAborted()
      if (existing) result = existing
      else {
        try {
          result = await api('/sites', {
            method: 'POST',
            headers,
            body: JSON.stringify({
              requestId: requestId.value,
              name: name.value,
              config,
              ...(props.initial.type !== 'managed' && database.value.instanceId
                ? { database: database.value }
                : {}),
              ...(props.initial.type !== 'managed' && certificateMode.value === 'upload'
                ? { certificate: certificate.value, privateKey: privateKey.value }
                : {}),
            }),
          })
        } catch (e) {
          signal.throwIfAborted()
          const found = await lookupSiteCreation(requestId.value, signal)
          if (!found) throw e
          result = found
        }
      }
      signal.throwIfAborted()
      saved.value = { id: result.siteId, revision: result.revision || 1 } as Resource
    }
    signal.throwIfAborted()
    await finishCreation(result.taskId, signal)
  } catch (e) {
    if (!isAbort(e)) error.value = errorText(e)
  } finally {
    runtimeFields.value?.clearCredentials()
    if (!signal.aborted) busy.value = false
  }
}
</script>
<template>
  <AppDialog
    v-model="visible"
    :title="title"
    width="min(760px,96vw)"
    :close-on-click-modal="false"
    destroy-on-close
    :close-on-press-escape="!busy"
    :show-close="!busy"
    class="site-create-dialog"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-alert
      v-if="saved?.id && databaseReady"
      title="关联数据库已创建，将继续使用该数据库。"
      type="info"
      :closable="false"
    />
    <el-form label-width="104px" class="site-create-form" @submit.prevent="save" :disabled="busy">
      <el-form-item v-if="['static', 'managed', 'app'].includes(form.type)" label="项目类型">
        <el-radio-group v-model="form.type" :disabled="!!editing">
          <el-radio-button value="managed">托管应用</el-radio-button>
          <el-radio-button value="static">静态网站</el-radio-button>
          <el-radio-button v-if="form.type === 'app'" value="app">外部应用</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="域名" required class="site-form-wide">
        <el-input
          v-model="form.domains"
          type="textarea"
          :rows="3"
          placeholder="每行一个域名，例如 example.com&#10;www.example.com"
          aria-label="绑定域名"
        />
        <small>多个域名绑定同一个项目；入口自动使用当前 Nginx。</small>
      </el-form-item>
      <el-form-item label="项目名称" required>
        <el-input
          v-model="name"
          @input="nameChanged = true"
          maxlength="63"
          placeholder="默认使用第一个域名"
        />
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="form.note" maxlength="1000" placeholder="可为空" />
      </el-form-item>
      <el-form-item v-if="form.type !== 'proxy'" label="根目录" required class="site-form-wide">
        <el-input
          v-model="form.rootDirectory"
          @input="rootChanged = true"
          :readonly="!!editing?.config.documentRoot"
          aria-label="根目录"
        >
          <template #append>
            <el-button :disabled="!!editing?.config.documentRoot" @click="browseDirectory">
              浏览
            </el-button>
          </template>
        </el-input>
        <small v-if="!editing">
          默认 {{ siteDirectory }}/域名，可修改项目文件夹名称。已有非空目录需确认纳管。
        </small>
        <el-checkbox v-if="!editing" v-model="form.adoptDirectory">
          使用已有目录（托管应用需预检并确认递归权限调整）
        </el-checkbox>
      </el-form-item>
      <template v-if="['proxy', 'app'].includes(form.type)">
        <el-form-item label="目标地址" required>
          <el-input v-model="form.upstream" placeholder="http://127.0.0.1:3000" />
        </el-form-item>
        <el-form-item label="发送 Host">
          <el-input v-model="form.proxyHost" placeholder="$http_host 或 upstream.example.com" />
        </el-form-item>
      </template>
      <template v-if="form.type === 'managed'">
        <el-form-item label="执行文件" required>
          <el-input v-model="form.executable" placeholder="绝对路径，例如 /usr/bin/node" />
          <small>上传已构建的程序或安装运行时后部署；参数不通过 Shell 执行。</small>
        </el-form-item>
        <el-form-item label="启动参数">
          <el-input v-model="form.arguments" placeholder='JSON 数组，例如 ["server.js"]' />
        </el-form-item>
        <SiteRuntimeFields ref="runtimeFields" v-model="form" :site="editing" />
        <el-form-item label="开机启动"><el-switch v-model="form.autoStart" /></el-form-item>
      </template>
      <el-form-item v-if="form.type === 'php'" label="PHP 版本" required>
        <el-select v-model="form.phpInstanceId" placeholder="请选择运行环境">
          <el-option label="静态" value="static" />
          <el-option
            v-for="r in php"
            :key="r.id"
            :label="r.config.version + ' · ' + r.name"
            :value="r.id"
          />
        </el-select>
        <el-link
          v-if="!php.length"
          type="primary"
          @click="
            () => {
              visible = false
              $router.push('/software?software=php')
            }
          "
        >
          前往安装 PHP
        </el-link>
      </el-form-item>
      <el-form-item v-if="editing || initial.type !== 'managed'" label="SSL 证书">
        <el-select v-model="certificateMode">
          <el-option
            :label="editing?.config.tls ? '保留当前证书' : '暂不部署（建站后可申请免费证书）'"
            value="none"
          />
          <el-option label="上传证书" value="upload" />
        </el-select>
      </el-form-item>
      <template v-if="certificateMode === 'upload' && (editing || initial.type !== 'managed')">
        <el-form-item label="PEM 证书链">
          <el-input v-model="certificate" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="PEM 私钥">
          <el-input v-model="privateKey" type="textarea" :rows="3" />
        </el-form-item>
      </template>
      <el-form-item v-if="!saved && initial.type !== 'managed'" label="数据库">
        <el-select
          v-model="database.instanceId"
          placeholder="不创建"
          @change="
            () => {
              database.encoding =
                databaseInstances.find((r) => r.id === database.instanceId)?.config.software ===
                'mysql'
                  ? 'utf8mb4'
                  : 'UTF8'
            }
          "
        >
          <el-option label="不创建" value="" />
          <el-option
            v-for="r in databaseInstances"
            :key="r.id"
            :value="r.id"
            :label="
              (r.config.software === 'mysql' ? 'MySQL' : 'PostgreSQL') +
              ' ' +
              r.config.version +
              ' · ' +
              r.name
            "
          />
        </el-select>
      </el-form-item>
      <template v-if="database.instanceId && !saved && initial.type !== 'managed'">
        <el-form-item label="数据库名" required>
          <el-input v-model="database.name" autocomplete="off" />
        </el-form-item>
        <el-form-item label="专用账号" required>
          <el-input v-model="database.username" autocomplete="off" />
        </el-form-item>
        <el-form-item label="数据库密码" required>
          <el-input
            v-model="database.password"
            type="password"
            show-password
            autocomplete="new-password"
            placeholder="至少 12 位，仅本机访问"
          >
            <template #append><el-button @click="generatePassword()">生成</el-button></template>
          </el-input>
        </el-form-item>
        <el-form-item label="字符集/编码">
          <el-select v-model="database.encoding">
            <el-option
              v-for="v in databaseInstances.find((r) => r.id === database.instanceId)?.config
                .software === 'mysql'
                ? ['utf8mb4', 'utf8', 'latin1']
                : ['UTF8', 'LATIN1']"
              :key="v"
              :value="v"
              :label="v"
            />
          </el-select>
        </el-form-item>
      </template>
      <el-form-item label="网站分类">
        <el-select v-model="form.category">
          <el-option v-for="v in categories" :key="v" :value="v" :label="v" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button :disabled="busy" @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="save">
        {{ busy ? '创建中' : editing ? '保存' : '创建' }}
      </el-button>
    </template>
  </AppDialog>
  <AppDialog
    v-model="directoryOpen"
    :title="'选择 ' + siteDirectory + ' 项目目录'"
    width="min(560px,94vw)"
    append-to-body
  >
    <el-alert
      v-if="directoryError"
      :title="directoryError + '；也可直接输入新目录，部署时创建。'"
      type="info"
      :closable="false"
    />
    <el-table :data="directories" v-loading="directoryBusy">
      <el-table-column prop="name" label="目录" />
      <el-table-column label="操作" width="90">
        <template #default="{ row }">
          <el-button
            link
            type="primary"
            @click="
              () => {
                form.rootDirectory = row.path
                rootChanged = true
                directoryOpen = false
              }
            "
          >
            选择
          </el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="directoryPage"
      :page-size="100"
      :total="directoryTotal"
      layout="prev,pager,next"
      @current-change="browseDirectory"
    />
  </AppDialog>
</template>
<style scoped>
.site-create-form {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  width: 100%;
  padding: 4px;
  box-sizing: border-box;
}
.site-form-wide {
  grid-column: 1/-1;
}
@media (max-width: 700px) {
  .site-create-form {
    grid-template-columns: minmax(0, 1fr);
  }
}

.site-create-form :deep(.el-checkbox) {
  height: auto;
  white-space: normal;
  align-items: flex-start;
}
.site-create-form :deep(.el-checkbox__label) {
  white-space: normal;
  line-height: 1.6;
}
.site-create-form :deep(.el-select) {
  width: 100%;
}
.site-create-form small {
  display: block;
  width: 100%;
  color: var(--el-text-color-secondary);
  line-height: 1.6;
  margin-top: 4px;
}
.site-create-form :deep(.el-collapse) {
  margin-top: 4px;
}
@media (max-width: 600px) {
  .site-create-form {
    padding: 0;
  }
  .site-create-form :deep(.el-date-editor) {
    max-width: 100%;
  }
}
</style>
