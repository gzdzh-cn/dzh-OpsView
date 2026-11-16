import { createRouter, createWebHistory } from 'vue-router'
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: () => import('./views/Overview.vue'),
      meta: { title: '概览', caption: '服务器运行状态与运维动态' },
    },
    {
      path: '/sites',
      component: () => import('./views/Resources.vue'),
      props: { kind: 'site' },
      meta: { title: '网站', caption: '从一个域名，开始部署你的应用' },
    },
    {
      path: '/databases',
      component: () => import('./views/Resources.vue'),
      props: { kind: 'database' },
      meta: { title: '数据库', caption: '独立实例，清晰管理每一份数据' },
    },
    {
      path: '/docker',
      component: () => import('./views/Docker.vue'),
      meta: { title: 'Docker', caption: '容器、镜像与项目，尽在掌握' },
    },
    {
      path: '/apps',
      component: () => import('./views/Resources.vue'),
      props: { kind: 'app' },
      meta: { title: '应用进程', caption: '让你的服务持续、稳定地运行' },
    },
    {
      path: '/software',
      component: () => import('./views/Market.vue'),
      meta: { title: '软件与插件', caption: '按需安装，让服务器恰到好处' },
    },
    {
      path: '/files',
      component: () => import('./views/Files.vue'),
      meta: { title: '文件', caption: '管理站点、应用与数据库导入文件' },
    },
    {
      path: '/backups',
      component: () => import('./views/Backups.vue'),
      meta: { title: '备份与计划', caption: '给每一次改变，留一条退路' },
    },
    {
      path: '/system',
      component: () => import('./views/System.vue'),
      meta: { title: '系统与安全', caption: '明确每个服务，掌握每个开放端口' },
    },
    {
      path: '/settings',
      component: () => import('./views/Settings.vue'),
      meta: { title: '面板设置', caption: '让面板安全地为你工作' },
    },
    {
      path: '/tasks',
      component: () => import('./views/Tasks.vue'),
      meta: { title: '任务中心', caption: '每一步操作，都有迹可循' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
