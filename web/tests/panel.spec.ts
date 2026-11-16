import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
const username = 'e2e-admin',
  password = 'OpsView-e2e-only-Password-2026!'
test.beforeAll(async ({ request }) => {
  const status = await (await request.get('/api/v1/auth')).json()
  if (status.setupRequired) {
    const token = fs.readFileSync(path.resolve('../.opsview/e2e/setup.token'), 'utf8').trim()
    const response = await request.post('/api/v1/auth/setup', {
      data: { token, username, password },
    })
    expect(response.status()).toBe(201)
  }
})
test.beforeEach(async ({ page }) => {
  await page.goto('/')
  await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
  await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '进入控制台' }).click()
  await expect(page.getByRole('heading', { name: '概览', exact: true })).toBeVisible()
})
test('all management menus render without browser errors', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  for (const [url, title] of [
    ['/sites', '网站'],
    ['/databases', '数据库'],
    ['/docker', 'Docker'],
    ['/apps', '应用进程'],
    ['/software', '软件与插件'],
    ['/files', '文件'],
    ['/backups', '备份与计划'],
    ['/system', '系统与安全'],
    ['/settings', '面板设置'],
    ['/tasks', '任务中心'],
  ]) {
    await page.goto(url!)
    await expect(page.getByRole('heading', { name: title, exact: true, level: 1 })).toBeVisible()
    await expect(page.locator('.surface').first()).toBeVisible()
  }
  expect(errors).toEqual([])
})
test('invalid site never becomes a deployed resource', async ({ page }) => {
  await page.route('**/api/v1/resources', (route) =>
    route.request().method() === 'GET' ? route.fulfill({ json: [nginxFixture] }) : route.continue(),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: '其他项目', exact: true }).click()
  await page.getByRole('button', { name: '添加项目', exact: true }).click()
  await page.getByPlaceholder('字母、数字、横线或下划线，例如 my-app').fill('e2e-invalid-site')
  await page
    .getByPlaceholder('example.com www.example.com，以空格分隔')
    .fill('example.com;include /etc/passwd')
  await page.getByRole('button', { name: '创建并部署' }).click()
  await expect(page.locator('.el-dialog .el-alert')).toBeVisible()
  await expect(page.locator('.el-dialog')).toBeVisible()
})
test('queued installation reports executor unavailable instead of success', async ({ page }) => {
  // This scenario exercises task failure, independently of native host qualification.
  await page.route('**/api/v1/catalog', async (route) => {
    const response = await route.fetch()
    const entries = await response.json()
    await route.fulfill({
      json: entries.map((e: any) => ({
        ...e,
        disabledReason: '',
        availability: { source: '', binary: '' },
      })),
    })
  })
  await page.goto('/software')
  await page
    .locator('.software-list .el-table__body tr')
    .first()
    .getByRole('button', { name: '安装', exact: true })
    .click()
  await page
    .getByPlaceholder('字母、数字、横线或下划线，例如 my-app')
    .fill(`e2e-nginx-${Date.now()}`)
  await page.getByRole('button', { name: '创建并部署' }).click()
  await expect(page.locator('.el-dialog')).not.toBeVisible()
  await page.goto('/tasks')
  await expect(page.locator('.el-table .status-badge.failed').first()).toBeVisible({
    timeout: 20000,
  })
  await page.locator('.el-table__body tr').first().click()
  await expect(page.locator('.el-drawer .log-box')).toContainText('执行器不可用')
})
test('desktop and mobile layouts remain inside viewport', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto('/')
  await page.screenshot({ path: '../.opsview/screenshots/overview-desktop.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.mobile-menu')).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)
  expect(overflow).toBe(false)
  await page.screenshot({ path: '../.opsview/screenshots/overview-mobile.png', fullPage: true })
  await page.locator('.mobile-menu').click()
  await expect(page.locator('.sidebar')).toBeInViewport()
  await page.locator('.sidebar').getByRole('link', { name: '网站', exact: true }).click()
  await expect(page.getByRole('heading', { name: '网站', exact: true, level: 1 })).toBeVisible()
})

test('panel backup persists a task, streams resumable logs and downloads encrypted bytes', async ({
  page,
}) => {
  const auth = await (await page.request.get('/api/v1/auth')).json()
  const response = await page.request.post('/api/v1/settings/backup', {
    headers: { 'X-CSRF-Token': auth.csrf },
    data: {},
  })
  expect(response.status()).toBe(202)
  const task = await response.json()
  await expect
    .poll(async () => {
      const tasks = await (await page.request.get('/api/v1/tasks')).json()
      return tasks.find((item: any) => item.id === task.id)?.state
    })
    .toBe('succeeded')
  const events = await (await page.request.get(`/api/v1/tasks/${task.id}/events`)).json()
  expect(events.length).toBeGreaterThan(1)
  const stream = await page.request.get(`/api/v1/tasks/${task.id}/events?stream=true`, {
    headers: { 'Last-Event-ID': String(events[0].sequence) },
  })
  expect(stream.headers()['content-type']).toContain('text/event-stream')
  const eventText = await stream.text()
  expect(eventText).toContain('任务完成')
  expect(eventText).toContain('event: done')
  expect(eventText).not.toContain('任务开始')
  const backups = await (await page.request.get('/api/v1/settings/backups')).json()
  expect(backups.length).toBeGreaterThan(0)
  const download = await page.request.get(backups[0].url)
  expect(download.status()).toBe(200)
  expect(download.headers()['content-disposition']).toContain('.ovp')
  const bytes = await download.body()
  expect(bytes.subarray(0, 5).toString()).toBe('OVB1\n')
  expect(bytes.includes(Buffer.from(password))).toBe(false)
  await page.goto('/settings')
  await page.getByRole('tab', { name: '备份恢复', exact: true }).click()
  await expect(page.getByRole('button', { name: '立即备份面板配置', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: backups[0].name })).toBeVisible()
})

const nginxFixture = {
  id: 'nginx-fixture',
  kind: 'instance',
  name: 'nginx-main',
  status: 'ready',
  revision: 1,
  config: { software: 'nginx', version: '1.30.6', mode: 'native', port: 80 },
}
test('websites require Nginx and expose exact tab columns', async ({ page }) => {
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [] }))
  await page.goto('/sites')
  await expect(page.getByRole('button', { name: '前往软件市场安装' })).toBeVisible()
  await page.unroute('**/api/v1/resources')
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture] }))
  await page.reload()
  for (const [tab, headers] of [
    [
      'PHP 项目',
      ['网站名', '状态', '备份', '根目录', '日流量', '到期时间', '备注', 'SSL证书', '操作'],
    ],
    ['反向代理', ['域名', '状态', '代理地址', '日流量', '备注', 'SSL证书', '操作']],
    ['其他项目', ['项目名称', '状态', '运行端口', '根目录', '到期时间', '备注', 'SSL证书', '操作']],
  ] as const) {
    await page.getByRole('tab', { name: tab, exact: true }).click()
    await expect(page.locator('.site-table .el-table__header th .cell')).toHaveText([...headers])
  }
  await page.screenshot({ path: '../.opsview/screenshots/sites-tabs.png', fullPage: true })
})
test('proxy editor submits multiple paths and upstreams', async ({ page }) => {
  let submitted: any
  await page.route('**/api/v1/resources', async (route) => {
    if (route.request().method() === 'GET') return route.fulfill({ json: [nginxFixture] })
    submitted = route.request().postDataJSON()
    await route.fulfill({
      json: { ...submitted, id: 'proxy-fixture', status: 'pending', revision: 1 },
    })
  })
  await page.route('**/api/v1/resources/proxy-fixture/actions/*', (route) =>
    route.fulfill({ status: 202, json: { id: 'proxy-task' } }),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: '反向代理', exact: true }).click()
  await page.getByRole('button', { name: '添加反向代理', exact: true }).click()
  await page.getByPlaceholder('字母、数字、横线或下划线，例如 my-app').fill('proxy-editor-test')
  await page.getByPlaceholder('example.com www.example.com，以空格分隔').fill('proxy.example.com')
  await page.getByRole('button', { name: '添加路径规则', exact: true }).click()
  await page
    .locator('.proxy-route textarea')
    .first()
    .fill('http://127.0.0.1:3000\nhttp://127.0.0.1:3001')
  await page.getByRole('button', { name: '添加路径规则', exact: true }).click()
  const second = page.locator('.proxy-route').nth(1)
  await second.getByPlaceholder('/ 或 /api/').fill('/api/')
  await second.locator('textarea').fill('http://127.0.0.1:4000')
  await second.getByPlaceholder('$host 或 api.example.com').fill('api.example.com')
  await page.getByRole('button', { name: '创建并部署', exact: true }).click()
  await expect(page.locator('.el-dialog')).not.toBeVisible()
  expect(submitted.config.proxyRoutes[0].targets).toEqual([
    'http://127.0.0.1:3000',
    'http://127.0.0.1:3001',
  ])
  expect(submitted.config.proxyRoutes[1]).toMatchObject({
    path: '/api/',
    host: 'api.example.com',
    targets: ['http://127.0.0.1:4000'],
  })
})
test('software settings show version context and configuration conflict', async ({ page }) => {
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture] }))
  await page.route('**/api/v1/instances/nginx-fixture/settings', (route) =>
    route.fulfill({
      json: {
        software: 'nginx',
        version: '1.30.6',
        state: 'active',
        editable: true,
        reload: true,
        files: ['main'],
        associated: [],
      },
    }),
  )
  await page.route('**/api/v1/instances/nginx-fixture/configuration?*', (route) =>
    route.fulfill({
      json: {
        revision: 'a'.repeat(64),
        content: 'worker_processes auto;\nevents { worker_connections 1024; }\nhttp { }',
        values: { worker_processes: 'auto' },
        fields: [],
        history: [],
      },
    }),
  )
  await page.route('**/api/v1/instances/nginx-fixture/validate', (route) =>
    route.fulfill({ status: 409, json: { error: '配置已被外部程序修改，请刷新并比较差异' } }),
  )
  await page.goto('/software')
  await page
    .locator('.software-list .el-table__body tr')
    .filter({ hasText: 'Nginx' })
    .getByRole('button', { name: '设置', exact: true })
    .click()
  const dialog = page.locator('.software-settings')
  await expect(dialog).toContainText('NGINX 1.30.6')
  await dialog.locator('aside').getByRole('button', { name: '配置文件', exact: true }).click()
  await expect(dialog.locator('.cm-editor')).toBeVisible()
  await dialog.getByRole('button', { name: '校验配置', exact: true }).click()
  await expect(dialog.locator('.el-alert')).toContainText('外部程序修改')
  await page.screenshot({ path: '../.opsview/screenshots/software-settings.png', fullPage: true })
})
test('panel settings have five tabs and stage HTTPS changes', async ({ page }) => {
  await page.route('**/api/v1/settings/runtime', async (route) => {
    if (route.request().method() === 'PUT') {
      const payload = route.request().postDataJSON()
      expect(payload.config.protocol).toBe('http')
      expect(payload.confirmedImpact).toBe(true)
      await route.fulfill({
        json: { url: 'http://panel.example.com:18790', confirmToken: 'a'.repeat(64) },
      })
    } else
      await route.fulfill({
        json: {
          config: {
            protocol: 'https',
            listenHost: '0.0.0.0',
            port: 18790,
            publicHost: 'panel.example.com',
          },
        },
      })
  })
  await page.goto('/settings')
  await expect(page.getByRole('tab')).toHaveText([
    '常用设置',
    '面板设置',
    '安全设置',
    '界面设置',
    '备份恢复',
  ])
  await page.getByRole('tab', { name: '面板设置', exact: true }).click()
  await page.getByText('关闭 SSL', { exact: true }).click()
  await page.getByRole('button', { name: '保存并重新连接', exact: true }).click()
  await page.getByRole('button', { name: '确认修改', exact: true }).click()
  await expect(page.locator('.el-message-box')).toBeHidden()
  await expect(page.getByRole('link', { name: '新面板登录地址（点击确认）' })).toHaveAttribute(
    'href',
    /^http:.*#runtime-confirm=/,
  )
  await page.screenshot({
    path: '../.opsview/screenshots/panel-settings.png',
    fullPage: true,
    animations: 'disabled',
  })
})
