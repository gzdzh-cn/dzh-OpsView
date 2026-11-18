<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { version as panelVersion } from '../package.json'
import { useRoute } from 'vue-router'
import {
  Monitor,
  Connection,
  Coin,
  Box,
  Grid,
  Folder,
  Clock,
  Lock,
  Setting,
  Bell,
  ArrowRight,
  List,
  Menu,
  Close,
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { navigating, prefetchPage, navigationError, reloadFailedPage } from './router'
import type { Dataset } from './stores/panel'
import PageContent from './components/PageContent.vue'
import { usePanel } from './stores/panel'
import { api, post, errorText } from './lib/api'
import Login from './views/Login.vue'
const panel = usePanel(),
  route = useRoute(),
  collapsed = ref(false),
  fatal = ref('')
const nav = [
  { path: '/', label: '概览', icon: Monitor },
  { path: '/sites', label: '网站', icon: Connection },
  { path: '/databases', label: '数据库', icon: Coin },
  { path: '/docker', label: 'Docker', icon: Box },
  { path: '/software', label: '插件市场', icon: Grid },
  { path: '/files', label: '文件', icon: Folder },
  { path: '/backups', label: '备份与计划', icon: Clock },
  { path: '/system', label: '系统与安全', icon: Lock },
  { path: '/settings', label: '面板设置', icon: Setting },
]
const running = computed(
  () => panel.tasks.filter((t) => ['queued', 'running'].includes(t.state)).length,
)
const datasets = computed<Dataset[]>(() => {
  if (route.path === '/') return ['resources', 'tasks', 'overview']
  if (route.path === '/software') return ['resources', 'tasks', 'catalog']
  if (['/files', '/settings', '/tasks'].includes(route.path)) return ['tasks']
  return ['resources', 'tasks']
})
const pageErrors = computed(() => datasets.value.filter((k) => panel.errors[k]))
const pageLoading = computed(() => datasets.value.some((k) => panel.pending[k]))
function refresh(force = false) {
  if (!panel.auth.authenticated || document.hidden) return
  panel.retain(datasets.value)
  void panel.refresh(
    datasets.value.filter((k) => k !== 'catalog'),
    force,
  )
  if (datasets.value.includes('catalog')) void panel.refresh(['catalog'], false)
  if (force) window.dispatchEvent(new Event('opsview:refresh-page'))
}
watch(
  () => route.path,
  () => refresh(),
)
async function ready() {
  const confirmToken = new URLSearchParams(location.hash.slice(1)).get('runtime-confirm')
  if (confirmToken) {
    await post('/runtime/confirm', { token: confirmToken })
    history.replaceState(null, '', location.pathname)
  }

  await panel.session()
  fatal.value = ''
  if (panel.auth.authenticated) {
    refresh()
    void api('/settings')
      .then((prefs) => setRefresh(prefs.refreshSeconds || 15))
      .catch(() => {})
  }
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
  panel.clear()
  panel.auth.authenticated = false
}
let timer: ReturnType<typeof setInterval>
function setRefresh(seconds: number) {
  clearInterval(timer)
  timer = setInterval(() => {
    refresh(true)
  }, seconds * 1000)
}
function visible() {
  if (!document.hidden) refresh(true)
}
function preferences(e: Event) {
  setRefresh((e as CustomEvent).detail.refreshSeconds || 15)
}
onMounted(async () => {
  document.addEventListener('visibilitychange', visible)
  window.addEventListener('opsview:unauthorized', unauthorized)
  window.addEventListener('opsview:preferences', preferences)
  try {
    await ready()
  } catch (e) {
    fatal.value = errorText(e)
    panel.loaded = true
  }
  if (!timer) setRefresh(15)
})
onUnmounted(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', visible)
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
        </span>
      </a>
      <nav>
        <router-link
          v-for="item in nav"
          :key="item.path"
          :to="item.path"
          :class="{ active: route.path === item.path }"
          @click="collapsed = false"
          @mouseenter="prefetchPage(item.path)"
          @focus="prefetchPage(item.path)"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>

          <span v-if="route.path === item.path" class="active-dot"></span>
        </router-link>
      </nav>
      <div class="sidebar-bottom">
        <router-link
          to="/tasks"
          @mouseenter="prefetchPage('/tasks')"
          @focus="prefetchPage('/tasks')"
        >
          <el-icon><List /></el-icon>
          <span>任务中心</span>
          <b v-if="running">{{ running }}</b>
          <el-icon v-else><ArrowRight /></el-icon>
        </router-link>
        <el-dropdown class="sidebar-account" @command="logout">
          <button class="account-button" aria-label="账号菜单">
            <span>{{ panel.auth.username }}</span>
            <span>⌄</span>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
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
      <header v-if="route.path === '/'" class="topbar">
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
      <main
        class="main-content"
        :aria-label="String(route.meta.title || '')"
        :aria-busy="pageLoading || navigating"
      >
        <button
          class="mobile-menu icon-button"
          aria-label="打开导航菜单"
          @click="collapsed = !collapsed"
        >
          <el-icon>
            <Close v-if="collapsed" />
            <Menu v-else />
          </el-icon>
        </button>
        <div
          v-if="navigating"
          class="navigation-progress"
          role="status"
          aria-label="正在切换页面"
        ></div>
        <el-alert
          v-if="navigationError"
          :title="navigationError"
          type="error"
          :closable="false"
          class="page-alert"
        >
          <el-button link @click="reloadFailedPage">重新加载页面</el-button>
        </el-alert>
        <el-alert
          v-for="key in pageErrors"
          :key="key"
          :title="panel.errors[key]"
          type="error"
          :closable="false"
          class="page-alert"
        >
          <el-button link @click="panel.refresh([key])">重试</el-button>
        </el-alert>
        <el-skeleton
          v-if="pageLoading"
          :rows="2"
          animated
          class="page-skeleton"
          aria-label="正在加载页面数据"
        />
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
        <router-view v-slot="{ Component }">
          <PageContent :key="route.path"><component :is="Component" /></PageContent>
        </router-view>
      </main>
    </div>
  </div>
</template>
