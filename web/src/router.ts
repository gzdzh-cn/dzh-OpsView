import { ref } from 'vue'
import { createRouter, createWebHistory, type RouteComponent } from 'vue-router'
const pages: Record<string, () => Promise<RouteComponent>> = {
  '/': () => import('./views/Overview.vue').then((module) => module.default),
  '/sites': () => import('./views/Sites.vue').then((module) => module.default),
  '/databases': () => import('./views/Databases.vue').then((module) => module.default),
  '/docker': () => import('./views/Docker.vue').then((module) => module.default),
  '/software': () => import('./views/Market.vue').then((module) => module.default),
  '/files': () => import('./views/Files.vue').then((module) => module.default),
  '/backups': () => import('./views/Backups.vue').then((module) => module.default),
  '/system': () => import('./views/System.vue').then((module) => module.default),
  '/settings': () => import('./views/Settings.vue').then((module) => module.default),
  '/tasks': () => import('./views/Tasks.vue').then((module) => module.default),
}
export const navigating = ref(false)
export const navigationError = ref('')
let failedPath = '/'
export function reloadFailedPage() {
  window.location.assign(failedPath)
}
export function prefetchPage(path: string) {
  void pages[path]?.().catch(() => {})
}
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: pages['/'],
      meta: { title: '概览', caption: '服务器运行状态与运维动态' },
    },
    {
      path: '/sites',
      component: pages['/sites'],
      meta: { title: '网站', caption: '从一个域名，开始部署你的应用' },
    },
    {
      path: '/databases',
      component: pages['/databases'],
      meta: { title: '数据库', caption: '独立实例，清晰管理每一份数据' },
    },
    {
      path: '/docker',
      component: pages['/docker'],
      meta: { title: 'Docker', caption: '容器、镜像与项目，尽在掌握' },
    },
    { path: '/apps', redirect: '/sites?tab=other&retired=apps' },
    {
      path: '/software',
      component: pages['/software'],
      meta: { title: '软件与插件', caption: '按需安装，让服务器恰到好处' },
    },
    {
      path: '/files',
      component: pages['/files'],
      meta: { title: '文件', caption: '管理服务器目录、文件与回收站' },
    },
    {
      path: '/backups',
      component: pages['/backups'],
      meta: { title: '备份与计划', caption: '给每一次改变，留一条退路' },
    },
    {
      path: '/system',
      component: pages['/system'],
      meta: { title: '系统与安全', caption: '明确每个服务，掌握每个开放端口' },
    },
    {
      path: '/settings',
      component: pages['/settings'],
      meta: { title: '面板设置', caption: '让面板安全地为你工作' },
    },
    {
      path: '/tasks',
      component: pages['/tasks'],
      meta: { title: '任务中心', caption: '每一步操作，都有迹可循' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(() => {
  navigationError.value = ''
  navigating.value = true
})
router.afterEach((to) => {
  navigating.value = false
  document.title = `${to.meta.title || 'OpsView'} · OpsView`
})
router.onError((_error, to) => {
  navigating.value = false
  failedPath = to.fullPath
  navigationError.value = '页面资源加载失败，请重新加载。'
})
