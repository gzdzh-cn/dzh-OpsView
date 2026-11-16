<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { version as panelVersion } from '../package.json'
import { useRoute } from 'vue-router'
import {
  Monitor,
  Connection,
  Coin,
  Box,
  Cpu,
  Grid,
  Folder,
  Clock,
  Lock,
  Setting,
  Bell,
  ArrowRight,
  RefreshRight,
  List,
  Menu,
  Close,
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { usePanel } from './stores/panel'
import { api, post, errorText } from './lib/api'
import Login from './views/Login.vue'
const panel = usePanel(),
  route = useRoute(),
  collapsed = ref(false),
  refreshing = ref(false),
  fatal = ref('')
const nav = [
  { path: '/', label: '概览', icon: Monitor },
  { path: '/sites', label: '网站', icon: Connection },
  { path: '/databases', label: '数据库', icon: Coin },
  { path: '/docker', label: 'Docker', icon: Box },
  { path: '/apps', label: '应用进程', icon: Cpu },
  { path: '/software', label: '软件与插件', icon: Grid },
  { path: '/files', label: '文件', icon: Folder },
  { path: '/backups', label: '备份与计划', icon: Clock },
  { path: '/system', label: '系统与安全', icon: Lock },
  { path: '/settings', label: '面板设置', icon: Setting },
]
const running = computed(
  () => panel.tasks.filter((t) => ['queued', 'running'].includes(t.state)).length,
)
async function refresh() {
  refreshing.value = true
  try {
    await panel.refresh()
    fatal.value = ''
  } catch (e) {
    fatal.value = errorText(e)
  } finally {
    refreshing.value = false
  }
}
async function ready() {
  const confirmToken = new URLSearchParams(location.hash.slice(1)).get('runtime-confirm')
  if (confirmToken) {
    history.replaceState(null, '', location.pathname)
    await post('/runtime/confirm', { token: confirmToken })
  }

  await panel.session()
  if (panel.auth.authenticated) await refresh()
}
async function logout() {
  try {
    await post('/auth/logout')
    await panel.session()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function unauthorized() {
  panel.auth.authenticated = false
}
let timer: ReturnType<typeof setInterval>
function setRefresh(seconds: number) {
  clearInterval(timer)
  timer = setInterval(() => {
    if (panel.auth.authenticated) void refresh()
  }, seconds * 1000)
}
function preferences(e: Event) {
  setRefresh((e as CustomEvent).detail.refreshSeconds || 15)
}
onMounted(async () => {
  window.addEventListener('opsview:unauthorized', unauthorized)
  window.addEventListener('opsview:preferences', preferences)
  try {
    await ready()
  } catch (e) {
    fatal.value = errorText(e)
    panel.loaded = true
  }
  const prefs = panel.auth.authenticated ? await api('/settings').catch(() => ({})) : {}
  setRefresh(prefs.refreshSeconds || 15)
})
onUnmounted(() => {
  clearInterval(timer)
  window.removeEventListener('opsview:unauthorized', unauthorized)
  window.removeEventListener('opsview:preferences', preferences)
})
</script>
<template>
  <div v-if="!panel.loaded" class="initial-loading">
    <div class="brand-symbol">O</div>
    <span>正在连接 OpsView…</span>
  </div>
  <Login v-else-if="!panel.auth.authenticated" :connection-error="fatal" @ready="ready" />
  <div v-else class="app-shell" :class="{ 'nav-open': collapsed }">
    <div v-if="collapsed" class="nav-shade" @click="collapsed = false"></div>
    <aside class="sidebar">
      <a href="/" class="brand">
        <span class="brand-symbol">O</span>
        <span>
          OpsView
          <span class="brand-dot">.</span>
          <small>SERVER CONTROL PANEL</small>
        </span>
      </a>
      <div class="sidebar-label">工作空间</div>
      <nav>
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          :class="{ active: route.path === item.path }"
          @click="collapsed = false"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
          <span v-if="item.path === '/software'" class="new-badge">NEW</span>
          <span v-if="route.path === item.path" class="active-dot"></span>
        </router-link>
      </nav>
      <div class="sidebar-bottom">
        <router-link to="/tasks">
          <el-icon><List /></el-icon>
          <span>任务中心</span>
          <b v-if="running">{{ running }}</b>
          <el-icon v-else><ArrowRight /></el-icon>
        </router-link>
        <div class="sidebar-server">
          <span class="status-dot"></span>
          <div>
            <strong>{{ panel.overview?.host?.hostname || '本地服务器' }}</strong>
            <small>{{ panel.auth.development ? '本地开发环境' : '单机管理 · 已连接' }}</small>
          </div>
          <span class="version">v{{ panelVersion }}</span>
        </div>
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <div class="breadcrumb">
          <button class="mobile-menu icon-button" @click="collapsed = !collapsed">
            <el-icon>
              <Close v-if="collapsed" />
              <Menu v-else />
            </el-icon>
          </button>
          <span>工作空间</span>
          <el-icon><ArrowRight /></el-icon>
          <strong>{{ route.meta.title }}</strong>
        </div>
        <div class="topbar-right">
          <span class="server-chip">
            <span class="status-dot"></span>
            {{ panel.overview?.host?.platform || 'localhost' }}
          </span>
          <router-link to="/tasks" class="notification icon-button" aria-label="任务通知">
            <el-icon><Bell /></el-icon>
            <i v-if="running"></i>
          </router-link>
          <div class="topbar-divider"></div>
          <el-dropdown @command="logout">
            <div class="profile">
              <span class="avatar">{{ (panel.auth.username || 'A')[0]?.toUpperCase() }}</span>
              <span>{{ panel.auth.username }}</span>
            </div>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>
      <main class="main-content">
        <div class="page-heading">
          <div>
            <div class="eyebrow">
              {{ route.path === '/' ? 'YOUR SERVER, AT A GLANCE' : 'OPSVIEW WORKSPACE' }}
            </div>
            <h1>{{ route.meta.title }}</h1>
            <p>{{ route.meta.caption }}</p>
          </div>
          <button class="secondary-button" :disabled="refreshing" @click="refresh">
            <el-icon :class="{ spinning: refreshing }"><RefreshRight /></el-icon>
            <span>刷新状态</span>
          </button>
        </div>
        <el-alert
          v-if="fatal"
          :title="fatal"
          type="error"
          show-icon
          :closable="false"
          class="page-alert"
        />
        <div v-if="panel.auth.development" class="development-banner">
          <span class="outlined-dot"></span>
          <span>本地开发环境</span>
          <span class="muted">
            资源配置与任务真实保存；Linux 执行器未连接时，系统操作会明确报错。
          </span>
        </div>
        <router-view :key="route.path" />
        <footer class="page-footer">
          <span>
            OpsView
            <span class="muted">/</span>
            让部署简单一点
          </span>
          <span>
            GoFrame + Vue 3
            <span class="muted">·</span>
            v{{ panelVersion }}
          </span>
        </footer>
      </main>
    </div>
  </div>
</template>
