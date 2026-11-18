<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { usePanel } from '../stores/panel'
import { action, post, put, errorText, type Config, type Resource } from '../lib/api'
import SiteRuntimeOptions from './SiteRuntimeOptions.vue'
const props = defineProps<{ modelValue: boolean; initial: Config; editing?: Resource }>()
const emit = defineEmits(['update:modelValue'])
const panel = usePanel()
const visible = computed({ get: () => props.modelValue, set: (v) => emit('update:modelValue', v) })
const form = ref<Config>({}),
  name = ref(''),
  busy = ref(false),
  error = ref(''),
  advanced = ref<string[]>([])
const certificate = ref(''),
  privateKey = ref(''),
  certificateMode = ref('none')
const saved = ref<Resource>()
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
      path: '/www/wwwroot',
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
  (open) => {
    if (!open) {
      certificate.value = ''
      privateKey.value = ''
      form.value.environment = ''
      return
    }
    form.value = JSON.parse(
      JSON.stringify(
        props.editing?.config || {
          type: 'php',
          domains: '',
          rootDirectory: '/www/wwwroot',
          upstream: 'http://127.0.0.1:3000',
          proxyHost: '$http_host',
          note: '',
          securityHeaders: true,
          denyFrames: false,
          denyUploadPHP: true,
          autoStart: true,
          port: 3000,
          user: 'isolated',
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
    error.value = ''
    busy.value = false
    advanced.value = []
    certificateMode.value = 'none'
    if (form.value.type === 'php' && !form.value.phpInstanceId)
      form.value.phpInstanceId = php.value[0]?.id
  },
)
watch(firstDomain, (domain) => {
  if (props.editing || saved.value) return
  if (!nameChanged.value) name.value = domain.slice(0, 63)
  if (!rootChanged.value)
    form.value.rootDirectory =
      '/www/wwwroot' + (/^[a-z0-9][a-z0-9.-]*$/i.test(domain) ? '/' + domain : '')
})
async function save() {
  error.value = ''
  busy.value = true
  try {
    if (!name.value || !form.value.domains.trim()) throw new Error('请填写域名及项目名称')
    if (form.value.type === 'php' && !form.value.phpInstanceId)
      throw new Error('请先安装并选择 PHP 版本')
    if (
      form.value.type !== 'proxy' &&
      !props.editing &&
      form.value.rootDirectory === '/www/wwwroot'
    )
      throw new Error('请填写域名以生成项目文件夹')
    if (certificateMode.value === 'upload' && (!certificate.value || !privateKey.value))
      throw new Error('请填写证书链与私钥')
    const config: Config = {
      ...form.value,
      domains: form.value.domains.trim().split(/\s+/).join(' '),
    }
    // Deployed paths are executor-owned. rootDirectory is only a new-site request.
    if (props.editing) config.rootDirectory = props.editing.config.rootDirectory
    if (config.type === 'proxy') delete config.rootDirectory
    let resource = saved.value
    if (!resource) {
      resource = props.editing
        ? await put('/resources/' + props.editing.id, {
            name: name.value,
            config,
            revision: props.editing.revision,
          })
        : await post('/resources', { kind: 'site', name: name.value, config })
      saved.value = resource
    }
    await action(
      resource!.id,
      certificateMode.value === 'upload' ? 'certificate' : 'apply',
      certificateMode.value === 'upload'
        ? { certificate: certificate.value, privateKey: privateKey.value }
        : {},
    )
    ElMessage.success('部署任务已提交，可在任务中心查看日志')
    await panel.refresh()
    visible.value = false
  } catch (e) {
    error.value = errorText(e) + (saved.value ? '；配置已保存，重试仅提交部署任务。' : '')
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <el-dialog
    v-model="visible"
    :title="title"
    width="min(760px,96vw)"
    :close-on-click-modal="false"
    destroy-on-close
    class="site-create-dialog"
    top="5vh"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-form
      label-width="112px"
      class="site-create-form"
      @submit.prevent="save"
      :disabled="busy || !!saved"
    >
      <el-form-item v-if="['static', 'managed', 'app'].includes(form.type)" label="项目类型">
        <el-radio-group v-model="form.type" :disabled="!!editing">
          <el-radio-button value="managed">托管应用</el-radio-button>
          <el-radio-button value="static">静态网站</el-radio-button>
          <el-radio-button v-if="form.type === 'app'" value="app">外部应用</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="域名" required>
        <el-input
          v-model="form.domains"
          type="textarea"
          :rows="3"
          placeholder="每行一个域名，例如 example.com&#10;www.example.com"
          aria-label="绑定域名"
        />
        <small>多个域名绑定同一个项目；入口端口使用所选 Nginx 配置。</small>
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
      <el-form-item v-if="form.type !== 'proxy'" label="根目录" required>
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
          默认 /www/wwwroot/域名，可修改项目文件夹名称。已有非空目录需确认纳管。
        </small>
        <el-checkbox v-if="!editing" v-model="form.adoptDirectory">
          使用已有目录（保留文件，仅调整根目录所有者供运行用户使用）
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
        <el-form-item label="运行端口" required>
          <el-input-number v-model="form.port" :min="1024" :max="65535" />
          <small>由 Nginx 转发至本机此端口，无需对公网放行应用端口。</small>
        </el-form-item>
        <el-form-item label="运行用户">
          <el-select v-model="form.user">
            <el-option label="独立站点用户（自动创建）" value="isolated" />
            <el-option label="www-data" value="www-data" />
          </el-select>
        </el-form-item>
        <el-form-item label="开机启动"><el-switch v-model="form.autoStart" /></el-form-item>
      </template>
      <el-form-item v-if="form.type === 'php'" label="PHP 版本" required>
        <el-select v-model="form.phpInstanceId" placeholder="请选择已安装 PHP">
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
      <el-form-item label="Nginx 入口">
        <el-select v-model="form.instanceId">
          <el-option
            v-for="r in instances"
            :key="r.id"
            :label="r.name + ' · :' + r.config.port"
            :value="r.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="SSL 证书">
        <el-select v-model="certificateMode">
          <el-option
            :label="editing?.config.tls ? '保留当前证书' : '暂不部署（建站后可申请免费证书）'"
            value="none"
          />
          <el-option label="上传证书" value="upload" />
        </el-select>
      </el-form-item>
      <template v-if="certificateMode === 'upload'">
        <el-form-item label="PEM 证书链">
          <el-input v-model="certificate" type="textarea" :rows="3" />
        </el-form-item>
        <el-form-item label="PEM 私钥">
          <el-input v-model="privateKey" type="textarea" :rows="3" />
        </el-form-item>
      </template>
      <el-collapse v-model="advanced">
        <el-collapse-item title="高级设置" name="advanced">
          <el-form-item label="到期时间">
            <el-date-picker
              v-model="form.expiresAt"
              type="datetime"
              value-format="YYYY-MM-DDTHH:mm:ssZ"
              placeholder="永久有效"
              clearable
            />
          </el-form-item>
          <template v-if="form.type === 'managed'">
            <el-form-item label="环境变量">
              <el-input
                v-model="form.environment"
                type="textarea"
                :rows="3"
                placeholder='JSON 对象，例如 {"NODE_ENV":"production"}'
              />
            </el-form-item>
            <el-form-item label="健康检查">
              <el-input
                v-model="form.healthURL"
                :placeholder="'http://127.0.0.1:' + form.port + '/health'"
              />
            </el-form-item>
          </template>
          <SiteRuntimeOptions v-model="form" />
          <el-form-item label="Nginx 参数">
            <el-input
              v-model="form.advanced"
              type="textarea"
              :rows="3"
              placeholder="client_max_body_size 32m;"
            />
          </el-form-item>
        </el-collapse-item>
      </el-collapse>
    </el-form>
    <template #footer>
      <el-button :disabled="busy" @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="save">
        {{ saved ? '重试部署' : editing ? '保存并部署' : '创建并部署' }}
      </el-button>
    </template>
  </el-dialog>
  <el-dialog
    v-model="directoryOpen"
    title="选择 /www/wwwroot 项目目录"
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
  </el-dialog>
</template>
<style scoped>
:global(.site-create-dialog) {
  display: flex;
  flex-direction: column;
  max-height: 90vh;
  margin-bottom: 0;
}
:global(.site-create-dialog .el-dialog__body) {
  overflow: auto;
  min-height: 0;
}
:global(.site-create-dialog .el-dialog__header),
:global(.site-create-dialog .el-dialog__footer) {
  flex-shrink: 0;
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
.site-create-form {
  padding: 4px 12px 0 0;
}
.site-create-form :deep(.el-form-item) {
  margin-bottom: 14px;
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
  .site-create-form :deep(.el-form-item__label) {
    width: 84px !important;
  }
  .site-create-form :deep(.el-form-item__content) {
    margin-left: 0 !important;
    min-width: 0;
  }
  .site-create-form :deep(.el-date-editor) {
    max-width: 100%;
  }
}
</style>
