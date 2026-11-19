<script setup lang="ts">
import AppDialog from './AppDialog.vue'
import ScheduleCycle from './ScheduleCycle.vue'
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { usePanel } from '../stores/panel'
import { api, post, put, action, errorText, type Config, type Resource } from '../lib/api'
import CodeEditor from './CodeEditor.vue'
import SiteRuntimeOptions from './SiteRuntimeOptions.vue'
const props = defineProps<{
    modelValue: boolean
    kind: string
    editing?: Resource
    initial?: Config
  }>(),
  emit = defineEmits(['update:modelValue', 'saved'])
const panel = usePanel(),
  form = ref<Config>({}),
  name = ref(''),
  busy = ref(false),
  error = ref('')
const titles: Record<string, string> = {
  site: '网站',
  instance: '软件实例',
  database: '数据库',
  compose: 'Compose 项目',
  firewall: '防火墙规则',
  backup: '备份任务',
  schedule: '计划任务',
  repository: '插件仓库',
}
const defaults: Record<string, Config> = {
  site: {
    type: 'static',
    instanceId: '',
    domains: '',
    upstream: 'http://127.0.0.1:3000',
    email: '',
    acceptACMETerms: false,
    redirectHTTPS: true,
    advanced: '',
  },
  instance: { catalogId: '', mode: 'native', port: 80, password: '' },
  database: { instanceId: '', username: '', password: '', source: '127.0.0.1', allowRemote: false },
  compose: { yaml: '' },
  firewall: {
    ports: '8080',
    protocol: 'tcp',
    source: 'any',
    action: 'allow',
    scope: 'host',
    note: '',
  },
  backup: {
    targetId: '',
    destination: 'local',
    retention: 7,
    endpoint: '',
    bucket: '',
    region: 'us-east-1',
    accessKey: '',
    secretKey: '',
  },
  schedule: { targetId: '', operation: 'backup', cron: '0 3 * * *', enabled: true },
  repository: { url: '', keyId: '' },
}
watch(
  () => props.modelValue,
  (v) => {
    if (v) {
      name.value = props.editing?.name || ''
      form.value = JSON.parse(
        JSON.stringify(props.editing?.config || { ...defaults[props.kind], ...props.initial }),
      )
      error.value = ''
      if (props.kind === 'instance' && !form.value.catalogId) {
        form.value.catalogId = panel.catalog.find(
          (v) =>
            (!form.value.softwareFilter || v.software === form.value.softwareFilter) &&
            !v.disabledReason,
        )?.id
        selectVersion()
      }
    }
  },
)
const instances = computed(() =>
  panel
    .kind('instance')
    .filter((r) =>
      props.kind === 'site'
        ? r.config.software === 'nginx'
        : ['mysql', 'postgresql'].includes(r.config.software) &&
          (!props.initial?.softwareFilter || props.initial.softwareFilter === r.config.software),
    ),
)
const targets = computed(() =>
  panel.resources
    .filter((r) => r.status !== 'recycled')
    .filter((r) =>
      props.kind === 'schedule'
        ? r.kind === (form.value.operation === 'renew' ? 'site' : 'backup')
        : ['site', 'database', 'app'].includes(r.kind) ||
          (r.kind === 'instance' && r.config.software === 'redis' && r.config.mode === 'native'),
    ),
)
const version = computed(() => panel.catalog.find((v) => v.id === form.value.catalogId))
const catalogOptions = computed(() =>
  panel.catalog.filter(
    (v) => !form.value.softwareFilter || v.software === form.value.softwareFilter,
  ),
)
function occupied(v: Config) {
  return panel
    .kind('instance')
    .some((r) => r.config.software === v.software && r.config.version === v.version)
}
function selectVersion() {
  const v = panel.catalog.find((v) => v.id === form.value.catalogId)
  form.value.port =
    v?.software === 'php'
      ? 9000
      : v?.software === 'redis'
        ? 6379
        : v?.software === 'mysql'
          ? 3306
          : v?.software === 'postgresql'
            ? 5432
            : v?.software === 'nodejs'
              ? 0
              : 80
  form.value.mode = 'native'
  form.value.installMethod =
    v?.methods?.includes('binary') && !v?.availability?.binary
      ? 'binary'
      : v?.methods?.find((m: string) => !v?.availability?.[m]) ||
        v?.methods?.[0] ||
        (['mysql', 'nodejs'].includes(v?.software) ? 'binary' : 'source')
}
async function save() {
  if (!name.value && props.kind !== 'instance') {
    error.value = '请填写资源名称'
    return
  }
  busy.value = true
  error.value = ''
  try {
    if (props.kind === 'instance' && version.value && occupied(version.value))
      throw new Error('该版本已有实例，请管理已有实例；卸载保留数据的记录须先清理')
    if (
      props.kind === 'instance' &&
      form.value.mode === 'native' &&
      version.value?.availability?.[form.value.installMethod]
    )
      throw new Error(version.value.availability[form.value.installMethod])
    if (props.kind === 'instance' && version.value?.disabledReason)
      throw new Error(version.value.disabledReason)
    const value = { ...form.value }
    delete value.softwareFilter
    if (props.kind === 'instance' && ['nginx', 'nodejs'].includes(version.value?.software))
      delete value.password
    let resource: Resource
    if (props.editing)
      resource = await put(`/resources/${props.editing.id}`, {
        name: name.value,
        config: value,
        revision: props.editing.revision,
      })
    else resource = await post('/resources', { kind: props.kind, name: name.value, config: value })
    const initialAction: Record<string, string> = {
      site: 'apply',
      instance: 'install',
      database: props.editing ? 'access' : 'create',
      compose: 'apply',
      firewall: 'apply',
    }
    if (initialAction[props.kind]) {
      try {
        await action(resource.id, initialAction[props.kind])
        ElMessage.success('已提交，执行进度可在任务中心查看')
      } catch (e) {
        ElMessage.warning('配置已保存，任务未提交：' + errorText(e))
      }
    } else ElMessage.success('配置已保存')
    await panel.refresh()
    emit('saved', resource)
    emit('update:modelValue', false)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
const visible = computed({ get: () => props.modelValue, set: (v) => emit('update:modelValue', v) })
// API types are generated from the backend OpenAPI contract.
void api
</script>
<template>
  <AppDialog
    v-model="visible"
    :title="(editing ? '编辑' : '创建') + (titles[kind] || kind)"
    width="650px"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <div v-if="kind === 'instance'" class="notice">
      名称由软件与准确版本自动生成，同一准确版本只允许保留一个实例。
      实例拥有独立端口与数据目录。默认仅本机访问数据库，安装过程可在任务中心追踪。
    </div>
    <div v-if="kind === 'firewall'" class="notice warn">
      应用后需在 120 秒内确认，否则自动撤回。云安全组需在云控制台单独配置。
    </div>

    <el-alert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
      class="page-alert"
    />
    <el-form label-position="top" @submit.prevent="save">
      <div class="form-grid">
        <el-form-item v-if="kind !== 'instance'" label="名称" class="wide" required>
          <el-input
            v-model="name"
            placeholder="字母、数字、横线或下划线，例如 my-app"
            :disabled="!!editing && kind === 'database'"
            maxlength="63"
          />
        </el-form-item>
        <template v-if="kind === 'site'">
          <el-form-item label="Nginx 实例" required>
            <el-select v-model="form.instanceId" placeholder="先安装 Nginx">
              <el-option
                v-for="r in instances"
                :key="r.id"
                :label="r.name + ' · ' + r.config.version"
                :value="r.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="网站类型">
            <el-select v-model="form.type">
              <el-option label="PHP 项目" value="php" />
              <el-option
                v-if="editing?.config.type === 'app'"
                label="外部应用（历史项目）"
                value="app"
              />
              <el-option label="静态站点" value="static" />
              <el-option label="反向代理" value="proxy" />
            </el-select>
          </el-form-item>
          <el-form-item v-if="form.type === 'php'" label="PHP 运行环境" required>
            <el-select v-model="form.phpInstanceId" placeholder="先到软件市场安装 PHP">
              <el-option
                v-for="r in panel
                  .kind('instance')
                  .filter((r) => r.config.software === 'php' && r.config.mode === 'native')"
                :key="r.id"
                :value="r.id"
                :label="r.name + ' · ' + r.config.version"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="到期时间" class="wide">
            <el-date-picker
              v-model="form.expiresAt"
              type="datetime"
              value-format="YYYY-MM-DDTHH:mm:ssZ"
              placeholder="永久有效"
              clearable
            />
          </el-form-item>
          <el-form-item label="备注" class="wide">
            <el-input v-model="form.note" maxlength="1000" />
          </el-form-item>
          <el-form-item label="绑定域名" class="wide" required>
            <el-input
              v-model="form.domains"
              placeholder="example.com www.example.com，以空格分隔"
            />
          </el-form-item>
          <el-form-item
            v-if="['proxy', 'app'].includes(form.type)"
            label="代理目标"
            class="wide"
            required
          >
            <el-input v-model="form.upstream" placeholder="http://127.0.0.1:3000" />
          </el-form-item>
          <el-form-item label="高级 Nginx 参数" class="wide">
            <el-input
              v-model="form.advanced"
              type="textarea"
              :rows="3"
              placeholder="client_max_body_size 32m;&#10;proxy_read_timeout 60s;"
            />
            <small class="form-help">
              允许请求体大小、proxy/send 超时与 gzip；配置先校验再应用，保留历史版本。
            </small>
          </el-form-item>
          <SiteRuntimeOptions v-model="form" />
          <el-form-item label="ACME 联系邮箱" class="wide">
            <el-input v-model="form.email" placeholder="admin@example.com" />
          </el-form-item>
          <el-form-item class="wide">
            <el-checkbox v-model="form.acceptACMETerms">
              同意 Let's Encrypt 服务条款，用于申请与自动续期证书
            </el-checkbox>
          </el-form-item>
          <el-form-item class="wide">
            <el-checkbox v-model="form.redirectHTTPS">证书配置后自动重定向到 HTTPS</el-checkbox>
          </el-form-item>
        </template>
        <template v-if="kind === 'instance'">
          <el-form-item label="软件与版本" class="wide" required>
            <el-select v-model="form.catalogId" @change="selectVersion">
              <el-option
                v-for="v in catalogOptions"
                :key="v.id"
                :label="`${v.software} ${v.version}${occupied(v) ? '（已有实例）' : ''}`"
                :disabled="occupied(v)"
                :value="v.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item v-if="form.mode === 'native'" label="原生安装方式">
            <el-radio-group v-model="form.installMethod">
              <el-radio-button
                value="binary"
                :disabled="
                  !!version?.availability?.binary ||
                  !(
                    version?.methods ||
                    (['mysql', 'nodejs'].includes(version?.software) ? ['binary'] : ['source'])
                  ).includes('binary')
                "
              >
                极速安装
              </el-radio-button>
              <el-radio-button
                value="source"
                :disabled="
                  !!version?.availability?.source ||
                  !(
                    version?.methods ||
                    (['mysql', 'nodejs'].includes(version?.software) ? ['binary'] : ['source'])
                  ).includes('source')
                "
              >
                编译安装
              </el-radio-button>
            </el-radio-group>
          </el-form-item>
          <p v-if="version?.availability?.binary" class="form-help wide">
            极速安装不可用：{{ version.availability.binary }}
          </p>
          <el-alert
            v-if="version?.disabledReason"
            :title="version.disabledReason"
            type="warning"
            :closable="false"
          />
          <el-form-item v-if="version?.software !== 'nodejs'" label="实例端口" required>
            <el-input-number v-model="form.port" :min="1" :max="65535" />
          </el-form-item>
          <el-form-item
            v-if="
              version?.software === 'mysql' ||
              version?.software === 'postgresql' ||
              version?.software === 'redis'
            "
            label="数据库管理员密码"
            class="wide"
            required
          >
            <el-input
              v-model="form.password"
              type="password"
              show-password
              placeholder="至少 12 位，安装后加密保存"
            />
          </el-form-item>
          <div class="form-help wide">
            极速安装直接部署已验证运行包；编译安装以低权限用户构建主软件，不会自动切换方式。
          </div>
        </template>
        <template v-if="kind === 'database'">
          <el-form-item label="数据库实例" class="wide" required>
            <el-select
              v-model="form.instanceId"
              :disabled="!!editing"
              placeholder="先安装 MySQL 或 PostgreSQL"
            >
              <el-option
                v-for="r in instances"
                :key="r.id"
                :label="`${r.name} · ${r.config.software} ${r.config.version}`"
                :value="r.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="专用用户名" required>
            <el-input v-model="form.username" :disabled="!!editing" placeholder="myapp" />
          </el-form-item>
          <el-form-item label="用户密码" required>
            <el-input
              v-model="form.password"
              type="password"
              show-password
              placeholder="至少 12 位"
            />
          </el-form-item>
          <el-form-item label="字符集/编码">
            <el-input
              v-model="form.encoding"
              :placeholder="
                instances.find((i) => i.id === form.instanceId)?.config.software === 'mysql'
                  ? 'utf8mb4'
                  : 'UTF8'
              "
            />
          </el-form-item>
          <el-form-item
            v-if="instances.find((i) => i.id === form.instanceId)?.config.software === 'mysql'"
            label="排序规则"
          >
            <el-input v-model="form.collation" placeholder="实例默认" />
          </el-form-item>
          <el-form-item label="连接来源" class="wide">
            <el-input v-model="form.source" placeholder="127.0.0.1 或来源 IP/CIDR" />
          </el-form-item>
          <el-form-item class="wide">
            <el-checkbox v-model="form.allowRemote">
              明确开启远程访问，并创建对应限源防火墙规则
            </el-checkbox>
          </el-form-item>
          <div class="form-help wide">
            默认仅本机。远程访问需要已启用的 UFW；规则应用后在安全页面 120
            秒内确认。云安全组单独设置。
          </div>
        </template>

        <template v-if="kind === 'compose'">
          <el-form-item label="Compose 配置" class="wide" required>
            <div style="width: 100%">
              <CodeEditor v-model="form.yaml" />
              <p class="form-help">
                镜像须固定 @sha256 摘要。支持项目内挂载和命名卷，不允许特权容器、宿主机根目录或
                Docker Socket 挂载。
              </p>
            </div>
          </el-form-item>
        </template>
        <template v-if="kind === 'firewall'">
          <el-form-item label="规则范围">
            <el-select v-model="form.scope">
              <el-option label="宿主机（UFW）" value="host" />
              <el-option label="Docker 入站（DOCKER-USER）" value="docker" />
            </el-select>
          </el-form-item>
          <el-form-item label="动作">
            <el-radio-group v-model="form.action">
              <el-radio-button value="allow">放行</el-radio-button>
              <el-radio-button value="deny">阻止</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="端口或范围" required>
            <el-input v-model="form.ports" placeholder="8080 或 8000:8100" />
          </el-form-item>
          <el-form-item label="协议">
            <el-select v-model="form.protocol">
              <el-option label="TCP" value="tcp" />
              <el-option label="UDP" value="udp" />
            </el-select>
          </el-form-item>
          <el-form-item label="来源 IP / CIDR" class="wide" required>
            <el-input v-model="form.source" placeholder="any、192.0.2.1 或 2001:db8::/32" />
          </el-form-item>
          <el-form-item label="备注" class="wide"><el-input v-model="form.note" /></el-form-item>
        </template>
        <template v-if="kind === 'backup'">
          <el-form-item label="备份资源" class="wide" required>
            <el-select v-model="form.targetId">
              <el-option
                v-for="r in targets"
                :key="r.id"
                :label="`${r.name} · ${r.kind}`"
                :value="r.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="存储位置">
            <el-select v-model="form.destination">
              <el-option label="服务器本地" value="local" />
              <el-option label="S3 兼容存储" value="s3" />
            </el-select>
          </el-form-item>
          <el-form-item label="保留份数">
            <el-input-number v-model="form.retention" :min="1" :max="100" />
          </el-form-item>
          <template v-if="form.destination === 's3'">
            <el-form-item label="HTTPS Endpoint" class="wide">
              <el-input v-model="form.endpoint" placeholder="https://s3.example.com" />
            </el-form-item>
            <el-form-item label="Bucket"><el-input v-model="form.bucket" /></el-form-item>
            <el-form-item label="Region"><el-input v-model="form.region" /></el-form-item>
            <el-form-item label="Access Key">
              <el-input v-model="form.accessKey" type="password" show-password />
            </el-form-item>
            <el-form-item label="Secret Key">
              <el-input v-model="form.secretKey" type="password" show-password />
            </el-form-item>
          </template>
          <div class="notice wide">
            备份使用分块认证加密。请在服务器安全保存独立的 backup.key；仅有备份文件无法解密。
          </div>
        </template>
        <template v-if="kind === 'schedule'">
          <el-form-item label="任务类型">
            <el-select v-model="form.operation">
              <el-option label="执行备份" value="backup" />
              <el-option label="续期证书" value="renew" />
            </el-select>
          </el-form-item>
          <el-form-item label="启用"><el-switch v-model="form.enabled" /></el-form-item>
          <el-form-item label="目标资源" class="wide" required>
            <el-select v-model="form.targetId">
              <el-option v-for="r in targets" :key="r.id" :label="r.name" :value="r.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="Cron 表达式（五段）" class="wide" required>
            <ScheduleCycle v-model="form.cron" />
          </el-form-item>
          <p class="form-help wide">
            以服务器时区执行。例如 0 3 * * * 表示每天 03:00。支持备份与证书续期，不执行任意 Shell。
          </p>
        </template>
        <template v-if="kind === 'repository'">
          <el-form-item label="仓库索引地址" class="wide" required>
            <el-input v-model="form.url" placeholder="https://plugins.example.com/index.json" />
          </el-form-item>
          <el-form-item label="签名者标识" class="wide">
            <el-input v-model="form.keyId" placeholder="team-release" />
          </el-form-item>
          <p class="form-help wide">
            仓库条目分别使用 Ed25519 签名。通过服务器上的 opsview trust-key 命令导入公钥后再同步。
          </p>
        </template>
      </div>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="save">
        {{
          ['backup', 'schedule', 'repository'].includes(kind)
            ? '保存配置'
            : editing
              ? '保存并应用'
              : '创建并部署'
        }}
      </el-button>
    </template>
  </AppDialog>
</template>
