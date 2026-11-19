<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { version as panelVersion } from '../package.json'
import { useRoute, useRouter } from 'vue-router'
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
import ThemeSwitcher from './components/ThemeSwitcher.vue'
import TemporaryAccess from './components/TemporaryAccess.vue'
import PasswordRenewal from './components/PasswordRenewal.vue'
import { appearance, applyAppearance } from './lib/appearance'
const panel = usePanel(),
  route = useRoute(),
  router = useRouter(),
  collapsed = ref(false),
  fatal = ref('')
const menus = [
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
const nav = computed(() => menus.filter((m) => !(appearance.hiddenMenus || []).includes(m.path)))
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
  if (
    !panel.auth.authenticated ||
    panel.auth.temporary ||
    panel.auth.passwordExpired ||
    document.hidden
  )
    return
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
async function consumeConfirmation(name: string) {
  // Initial async navigation can finish after the confirmation response. Keep
  // router state and browser history in sync so it cannot restore a used token.
  await router.isReady()
  const hash = new URLSearchParams(route.hash.slice(1))
  hash.delete(name)
  await router.replace({
    path: route.path,
    query: route.query,
    hash: hash.size ? '#' + hash.toString() : '',
  })
}
async function ready() {
  const confirmToken = new URLSearchParams(location.hash.slice(1)).get('runtime-confirm')
  if (confirmToken) {
    await post('/runtime/confirm', { token: confirmToken })
    await consumeConfirmation('runtime-confirm')
  }

  void api('/public/preferences')
    .then(applyAppearance)
    .catch(() => {})
  await panel.session()
  const preferenceToken = new URLSearchParams(location.hash.slice(1)).get('preference-confirm')
  if (preferenceToken && panel.auth.authenticated) {
    await post('/settings/confirm', { token: preferenceToken })
    await consumeConfirmation('preference-confirm')
  }
  fatal.value = ''
  if (panel.auth.authenticated && !panel.auth.temporary && !panel.auth.passwordExpired) {
    refresh()
    void api('/settings')
      .then((prefs) => {
        setRefresh(prefs.refreshSeconds || 15)
        applyAppearance(prefs)
      })
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
  const patch = (e as CustomEvent).detail
  if (patch.refreshSeconds !== undefined) setRefresh(patch.refreshSeconds)
  applyAppearance(patch)
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
  <TemporaryAccess v-else-if="panel.auth.temporary" />
  <PasswordRenewal v-else-if="panel.auth.passwordExpired" />
  <div v-else class="app-shell" :class="{ 'nav-open': collapsed }">
    <div v-if="collapsed" class="nav-shade" @click="collapsed = false"></div>
    <aside class="sidebar">
      <a href="/" class="brand">
        <span class="brand-symbol">O</span>
        <span>
          {{ appearance.sidebarTitle || appearance.name || 'OpsView' }}
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
          <a
            href="https://github.com/gzdzh-cn/dzh-OpsView"
            target="_blank"
            rel="noopener noreferrer"
            class="repository-link icon-button"
            title="GitHub 项目仓库（新标签页打开）"
            aria-label="GitHub 项目仓库（新标签页打开）"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path
                d="M12 .75a11.25 11.25 0 0 0-3.558 21.923c.563.104.77-.244.77-.543 0-.267-.01-.975-.015-1.913-3.13.68-3.79-1.508-3.79-1.508-.512-1.3-1.25-1.646-1.25-1.646-1.022-.7.078-.686.078-.686 1.13.08 1.725 1.16 1.725 1.16 1.005 1.723 2.638 1.225 3.28.937.102-.728.393-1.225.715-1.507-2.498-.284-5.125-1.25-5.125-5.565 0-1.23.44-2.235 1.16-3.023-.117-.285-.503-1.43.11-2.98 0 0 .945-.303 3.095 1.155A10.79 10.79 0 0 1 12 6.175c.957.005 1.922.13 2.823.38 2.148-1.458 3.09-1.155 3.09-1.155.615 1.55.23 2.695.113 2.98.722.788 1.157 1.793 1.157 3.023 0 4.328-2.63 5.278-5.135 5.557.403.35.763 1.043.763 2.1 0 1.517-.014 2.74-.014 3.112 0 .302.203.652.773.542A11.252 11.252 0 0 0 12 .75Z"
              />
            </svg>
          </a>
          <ThemeSwitcher />
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
