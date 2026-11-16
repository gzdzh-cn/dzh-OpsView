<script setup lang="ts">
import type { Config } from '../lib/api'
import { computed } from 'vue'
import ProxyRouteFields from './ProxyRouteFields.vue'
const config = defineModel<Config>({ required: true })
type ProxyRoute = {
  path: string
  targets: string[]
  host: string
  websocket: boolean
  connectTimeout: number
  readTimeout: number
  sendTimeout: number
}
const routes = computed(() => (config.value.proxyRoutes || []) as ProxyRoute[])
function addRoute() {
  const items = routes.value
  items.push({
    path: items.length ? '/api' + items.length + '/' : '/',
    targets: [config.value.upstream || 'http://127.0.0.1:3000'],
    host: '$host',
    websocket: true,
    connectTimeout: 60,
    readTimeout: 60,
    sendTimeout: 60,
  })
  config.value.proxyRoutes = items
}
</script>
<template>
  <div v-if="['php', 'static'].includes(config.type)" class="site-runtime-options">
    <el-form-item label="运行目录（相对网站根目录）">
      <el-input v-model="config.runningDirectory" placeholder="留空为根目录；例如 public" />
    </el-form-item>
    <el-form-item label="默认文档">
      <el-input
        v-model="config.defaultDocuments"
        :placeholder="config.type === 'php' ? 'index.php index.html' : 'index.html'"
      />
    </el-form-item>
    <el-form-item label="伪静态模板">
      <el-select v-model="config.rewriteTemplate" placeholder="沿用默认路由规则" clearable>
        <el-option label="直接文件访问（不存在返回 404）" value="none" />
        <el-option
          v-if="config.type === 'php'"
          label="通用 PHP 前端控制器"
          value="front-controller"
        />
        <el-option v-if="config.type === 'php'" label="WordPress" value="wordpress" />
        <el-option v-if="config.type === 'php'" label="ThinkPHP" value="thinkphp" />
        <el-option
          v-if="config.type === 'php'"
          label="Laravel（运行目录请选择 public）"
          value="laravel"
        />
        <el-option
          v-if="config.type === 'static'"
          label="单页应用（回退 index.html）"
          value="spa"
        />
      </el-select>
    </el-form-item>
  </div>
  <div v-if="config.type === 'proxy'" class="site-runtime-options">
    <p class="form-help">
      路径规则启用后替代单一代理目标。多上游按轮询分发，保留请求路径，同组使用相同协议。HTTPS
      上游使用首个上游主机名进行 SNI。
    </p>
    <ProxyRouteFields
      v-for="(_, index) in routes"
      :key="index"
      v-model="routes[index]"
      :index="index"
      @remove="config.proxyRoutes.splice(index, 1)"
    />
    <el-button :disabled="(config.proxyRoutes || []).length >= 16" @click="addRoute">
      添加路径规则
    </el-button>
  </div>
</template>
<style scoped>
.site-runtime-options {
  width: 100%;
  grid-column: 1 / -1;
}
</style>
