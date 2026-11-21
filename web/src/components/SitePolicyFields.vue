<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Config } from '../lib/api'
import { randomID } from '../lib/browser'
import ProxyRouteFields from './ProxyRouteFields.vue'
import SiteRedirectFields from './SiteRedirectFields.vue'
const config = defineModel<Config>({ required: true })
defineProps<{ page: string }>()
const accessTab = ref('password'),
  otherTab = ref('compression')
const redirects = computed<Config[]>(() => config.value.redirects || [])
const proxies = computed<Config[]>(() => config.value.proxyRoutes || [])
const auth = computed(() => Object.entries(config.value.accessRules || {}) as [string, Config][])
function addAuth() {
  config.value.accessRules ||= {}
  config.value.accessRules[randomID()] = { path: '/', username: '', password: '' }
}
function addRedirect() {
  ;(config.value.redirects ||= []).push({
    path: '/old/',
    target: 'https://',
    code: 301,
    exact: false,
  })
}
function addProxy() {
  ;(config.value.proxyRoutes ||= []).push({
    path: config.value.type === 'proxy' && !config.value.proxyRoutes.length ? '/' : '/api/',
    targets: [config.value.upstream || 'http://127.0.0.1:3000'],
    host: '$host',
    websocket: true,
    connectTimeout: 60,
    readTimeout: 60,
    sendTimeout: 60,
  })
}
function defaults(key: string, value: unknown) {
  if (config.value[key] == null) config.value[key] = value
}
function limitChanged(on: boolean) {
  if (on) {
    defaults('maxConnections', 300)
    defaults('perIPConnections', 25)
    defaults('rateKB', 512)
  }
}
function hotlinkChanged(on: boolean) {
  if (on) {
    defaults('hotlinkExtensions', 'jpg jpeg gif png js css')
    defaults(
      'hotlinkDomains',
      String(config.value.domains || '')
        .split(/\s+/)
        .map((v) => v.split(':')[0])
        .join('\n'),
    )
  }
}
function cacheChanged(on: boolean) {
  if (on) {
    defaults('cacheExtensions', 'jpg jpeg gif png ico css js woff woff2')
    defaults('cacheSeconds', 86400)
  }
}
function corsChanged(on: boolean) {
  if (on) {
    defaults('corsOrigin', '*')
    defaults('corsMethods', 'GET, HEAD, POST, OPTIONS')
    defaults('corsHeaders', 'Content-Type, Authorization')
  }
}
const headers = computed(() => config.value.responseHeaders || {})
</script>
<template>
  <el-form label-width="104px" class="site-policy-form">
    <template v-if="page === 'access'">
      <el-tabs v-model="accessTab">
        <el-tab-pane label="加密访问" name="password" />
        <el-tab-pane label="禁止访问" name="deny" />
        <el-tab-pane label="IP 限制" name="ip" />
        <el-tab-pane label="双向认证" name="client" />
      </el-tabs>
      <template v-if="accessTab === 'password'">
        <p class="form-help">
          按目录设置 HTTP BasicAuth。密码以哈希保存；建议先启用
          HTTPS。更具体的目录规则优先，认证不保护 ACME 验证入口。
        </p>
        <el-button @click="addAuth" :disabled="auth.length >= 16">添加加密访问</el-button>
        <div v-for="[id, rule] in auth" :key="id" class="site-rule-block">
          <div class="page-toolbar">
            <strong>{{ rule.path || '新规则' }}</strong>
            <el-button link type="danger" @click="delete config.accessRules[id]">
              删除规则
            </el-button>
          </div>
          <el-form-item label="保护目录">
            <el-input v-model="rule.path" placeholder="/ 或 /admin" />
          </el-form-item>
          <el-form-item label="用户名">
            <el-input v-model="rule.username" autocomplete="off" />
          </el-form-item>
          <el-form-item label="访问密码">
            <el-input
              v-model="rule.password"
              type="password"
              autocomplete="new-password"
              placeholder="12–72 字节；不修改则保留"
            />
          </el-form-item>
        </div>
      </template>
      <template v-else-if="accessTab === 'deny'">
        <el-form-item label="禁止目录">
          <el-input
            v-model="config.denyPaths"
            type="textarea"
            :rows="5"
            placeholder="每行一个路径，例如 /private 或 /uploads/backup"
          />
        </el-form-item>
        <el-form-item label="禁止扩展名">
          <el-input v-model="config.denyExtensions" placeholder="例如 zip tar gz，用空格分隔" />
        </el-form-item>
        <p class="form-help">
          系统仍会保护本地网站的隐藏文件及敏感备份文件；目录限制也作用于代理路径。
        </p>
      </template>
      <template v-else-if="accessTab === 'ip'">
        <el-form-item label="允许来源">
          <el-input
            v-model="config.allowAddresses"
            type="textarea"
            :rows="4"
            placeholder="每行一个 IP/CIDR；留空允许所有来源"
          />
        </el-form-item>
        <el-form-item label="禁止来源">
          <el-input
            v-model="config.denyAddresses"
            type="textarea"
            :rows="4"
            placeholder="每行一个 IP/CIDR；禁止规则优先"
          />
        </el-form-item>
        <p class="form-help">
          以 Nginx 实际连接来源为准。经 CDN/上游代理访问时，请确认来源；配置不自动信任客户端转发头。
        </p>
      </template>
      <template v-else>
        <el-alert
          v-if="!config.tls"
          title="先在 SSL 证书中部署 HTTPS，再启用客户端证书认证"
          type="info"
          :closable="false"
        />
        <el-form-item label="双向认证">
          <el-switch v-model="config.clientAuthEnabled" :disabled="!config.tls" />
        </el-form-item>
        <el-form-item label="客户端 CA">
          <el-input
            v-model="config.clientCA"
            type="textarea"
            :rows="10"
            placeholder="粘贴用于验证客户端证书的 PEM CA 证书；不要上传私钥"
          />
        </el-form-item>
        <p class="form-help">
          启用后公众请求必须提供有效客户端证书，HTTP 访问被拒绝。CA 验证不能替代证书撤销服务；更换
          CA 前请核对所有客户端。ACME 及面板内部随机健康检查路径保留。
        </p>
      </template>
    </template>
    <template v-else-if="page === 'limits'">
      <el-form-item label="流量限制">
        <el-switch v-model="config.limitEnabled" @change="limitChanged" />
      </el-form-item>
      <template v-if="config.limitEnabled">
        <el-form-item label="站点并发">
          <el-input-number v-model="config.maxConnections" :min="1" :max="1000000" />
        </el-form-item>
        <el-form-item label="单 IP 并发">
          <el-input-number v-model="config.perIPConnections" :min="1" :max="100000" />
        </el-form-item>
        <el-form-item label="请求限速">
          <el-input-number v-model="config.rateKB" :min="0" :max="1048576" />
          <span class="form-help">KB/s，0 不限速</span>
        </el-form-item>
      </template>
      <p class="form-help">
        并发统计在同一站点各域名及端口间共享，超限返回
        429；带宽为单请求限速。统计口径为活动请求，并非月流量限额。
      </p>
    </template>
    <template v-else-if="page === 'redirects'">
      <el-form-item label="404 跳转">
        <el-input
          v-model="config.redirect404Target"
          placeholder="留空保留真实 404；填写完整 HTTP/HTTPS 目标"
        />
      </el-form-item>

      <div class="page-toolbar">
        <el-button @click="addRedirect" :disabled="(config.redirects || []).length >= 32">
          添加重定向
        </el-button>
        <small class="form-help">仅对匹配路径生效；目录前缀匹配优先于本地路由。</small>
      </div>
      <SiteRedirectFields
        v-for="(_, index) in redirects"
        :key="index"
        v-model="config.redirects[index]"
        :index="index"
        @remove="config.redirects.splice(index, 1)"
      />
      <el-empty
        v-if="!(config.redirects || []).length"
        description="尚未设置重定向"
        :image-size="48"
      />
    </template>
    <template v-else-if="page === 'proxy-rules'">
      <p class="form-help">
        本地站点可为 /api/ 等路径添加代理；根路径保留本地运行环境。访问限制和 IP 规则仍然生效。
      </p>
      <ProxyRouteFields
        v-for="(_, index) in proxies"
        :key="index"
        v-model="config.proxyRoutes[index]"
        :index="index"
        @remove="config.proxyRoutes.splice(index, 1)"
      />
      <el-button :disabled="(config.proxyRoutes || []).length >= 16" @click="addProxy">
        添加代理规则
      </el-button>
    </template>
    <template v-else-if="page === 'hotlink'">
      <el-form-item label="防盗链">
        <el-switch v-model="config.hotlinkEnabled" @change="hotlinkChanged" />
      </el-form-item>
      <el-form-item label="资源扩展名">
        <el-input v-model="config.hotlinkExtensions" placeholder="jpg jpeg gif png js css" />
      </el-form-item>
      <el-form-item label="允许来源">
        <el-input
          v-model="config.hotlinkDomains"
          type="textarea"
          :rows="5"
          placeholder="每行一个域名，不含协议；可使用 *.example.com"
        />
      </el-form-item>
      <el-form-item label="拒绝响应">
        <el-select v-model="config.hotlinkStatus" placeholder="403">
          <el-option :value="403" label="403 禁止访问" />
          <el-option :value="404" label="404 未找到" />
        </el-select>
      </el-form-item>
      <el-form-item label="空来源">
        <el-checkbox v-model="config.hotlinkAllowEmpty">允许无 Referer 的直接访问</el-checkbox>
      </el-form-item>
      <p class="form-help">
        匹配资源的来源不被允许时返回所选状态（默认 403）。不作为鉴权机制，也不验证客户端身份。
      </p>
    </template>
    <template v-else-if="page === 'other'">
      <el-tabs v-model="otherTab">
        <el-tab-pane label="压缩与缓存" name="compression">
          <el-form-item label="Gzip 压缩"><el-switch v-model="config.gzipEnabled" /></el-form-item>
          <el-form-item v-if="config.gzipEnabled" label="压缩等级">
            <el-input-number v-model="config.gzipLevel" :min="1" :max="9" placeholder="5" />
          </el-form-item>
          <el-form-item label="静态缓存">
            <el-switch v-model="config.cacheEnabled" @change="cacheChanged" />
          </el-form-item>
          <template v-if="config.cacheEnabled">
            <el-form-item label="资源扩展名">
              <el-input v-model="config.cacheExtensions" />
            </el-form-item>
            <el-form-item label="有效期">
              <el-input-number v-model="config.cacheSeconds" :min="1" :max="31536000" />
              <span class="form-help">秒；浏览器缓存，不缓存代理响应正文</span>
            </el-form-item>
          </template>
        </el-tab-pane>
        <el-tab-pane label="响应头" name="headers">
          <el-form-item label="安全响应头">
            <el-switch v-model="config.securityHeaders" />
          </el-form-item>
          <el-form-item label="禁止框架嵌入">
            <el-switch v-model="config.denyFrames" />
          </el-form-item>
          <el-form-item
            v-for="name in [
              'Content-Security-Policy',
              'Referrer-Policy',
              'X-Content-Type-Options',
              'X-Frame-Options',
              'Permissions-Policy',
            ]"
            :key="name"
            :label="name"
            class="site-header-field"
          >
            <el-input
              :model-value="headers[name]"
              @update:model-value="config.responseHeaders = { ...headers, [name]: $event }"
              :aria-label="name"
              placeholder="留空不覆盖默认策略"
            />
          </el-form-item>
        </el-tab-pane>
        <el-tab-pane label="跨域访问" name="cors">
          <el-form-item label="跨域访问">
            <el-switch v-model="config.corsEnabled" @change="corsChanged" />
          </el-form-item>
          <template v-if="config.corsEnabled">
            <el-form-item label="允许来源">
              <el-input v-model="config.corsOrigin" placeholder="https://example.com 或 *" />
            </el-form-item>
            <el-form-item label="允许方法"><el-input v-model="config.corsMethods" /></el-form-item>
            <el-form-item label="请求头"><el-input v-model="config.corsHeaders" /></el-form-item>
            <el-form-item label="携带凭据">
              <el-switch v-model="config.corsCredentials" />
            </el-form-item>
            <p class="form-help">
              启用后由 Nginx 返回 OPTIONS 预检响应。携带凭据时需指定单个来源，不能使用 *。
            </p>
          </template>
        </el-tab-pane>
      </el-tabs>
    </template>
  </el-form>
</template>
<style scoped>
.site-rule-block {
  margin-top: 12px;
  padding: 12px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: var(--el-border-radius-base);
}
.site-header-field {
  display: block;
}
.site-header-field :deep(.el-form-item__label) {
  width: auto !important;
  float: none;
  display: block;
  text-align: left;
}
.site-header-field :deep(.el-form-item__content) {
  margin-left: 0 !important;
}
</style>
