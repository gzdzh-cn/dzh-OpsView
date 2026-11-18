import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
const username = 'e2e-admin',
  password = 'OpsView-e2e-only-Password-2026!'
test.beforeAll(async ({ request }) => {
  const status = await (await request.get('/api/v1/auth')).json()
  if (status.setupRequired) {
    const token = fs
      .readFileSync(path.join(process.env.OPSVIEW_E2E_DATA!, 'setup.token'), 'utf8')
      .trim()
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
  await expect(page.getByRole('main', { name: '概览', exact: true })).toBeVisible()
})
test('sidebar navigates all embedded management pages without missing assets', async ({ page }) => {
  const errors: string[] = []
  const missingAssets: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('response', (response) => {
    if (new URL(response.url()).pathname.startsWith('/assets/') && !response.ok())
      missingAssets.push(`${response.status()} ${response.url()}`)
  })
  for (const [url, title] of [
    ['/sites', '网站'],
    ['/databases', '数据库'],
    ['/docker', 'Docker'],
    ['/software', '软件与插件'],
    ['/files', '文件'],
    ['/backups', '备份与计划'],
    ['/system', '系统与安全'],
    ['/settings', '面板设置'],
    ['/tasks', '任务中心'],
  ]) {
    await page.locator(`.sidebar a[href="${url}"]`).click()
    await expect(page).toHaveURL(url!)
    await expect(page.getByRole('main', { name: title, exact: true })).toBeVisible()
    await expect(page.locator('.surface').first()).toBeVisible()
    await page.screenshot({
      path: `../.opsview/screenshots/compact-${url!.slice(1)}.png`,
      animations: 'disabled',
      fullPage: true,
    })
  }
  await page.locator('.sidebar').getByRole('link', { name: '概览', exact: true }).click()
  await expect(page).toHaveURL('/')
  await expect(page.getByRole('main', { name: '概览', exact: true })).toBeVisible()
  expect(missingAssets).toEqual([])
  expect(errors).toEqual([])
})
test('invalid site never becomes a deployed resource', async ({ page }) => {
  await page.route('**/api/v1/resources', (route) =>
    route.request().method() === 'GET' ? route.fulfill({ json: [nginxFixture] }) : route.continue(),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: '其他项目', exact: true }).click()
  await page.getByRole('button', { name: '添加项目', exact: true }).click()
  await page.getByPlaceholder('默认使用第一个域名').fill('e2e-invalid-site')
  await page
    .getByRole('textbox', { name: '绑定域名', exact: true })
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
  await expect(page.locator('.software-list .el-select').first()).toContainText('1.30.6')
  await page
    .locator('.software-list .el-table__body tr')
    .first()
    .getByRole('button', { name: '安装', exact: true })
    .click()
  await expect(page.getByPlaceholder('字母、数字、横线或下划线，例如 my-app')).toHaveCount(0)
  await page.getByRole('button', { name: '创建并部署' }).click()
  await expect(page.locator('.el-dialog:visible')).toHaveCount(0)
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
  await expect(page.locator('.sidebar')).not.toBeInViewport()
  await page.screenshot({
    path: '../.opsview/screenshots/overview-mobile.png',
    fullPage: true,
    animations: 'disabled',
  })
  await page.locator('.mobile-menu').click()
  await expect(page.locator('.sidebar')).toBeInViewport()
  await page.locator('.sidebar').getByRole('link', { name: '网站', exact: true }).click()
  await expect(page.getByRole('main', { name: '网站', exact: true })).toBeVisible()
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
  await page.getByPlaceholder('默认使用第一个域名').fill('proxy-editor-test')
  await page.getByRole('textbox', { name: '绑定域名', exact: true }).fill('proxy.example.com')
  await page.getByRole('dialog').getByRole('button', { name: '高级设置' }).click()
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
  await expect(page.locator('.el-dialog:visible')).toHaveCount(0)
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

test('software logs filter, download and confirm archive scope', async ({ page }) => {
  const archive = '20261117T120000.123456789Z-abc123.log'
  const requests: URL[] = []
  let mutation: any
  await page.route('**/api/v1/tasks', (route) =>
    route.fulfill({ json: mutation ? [{ id: 'log-archive-task', state: 'succeeded' }] : [] }),
  )
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture] }))
  await page.route('**/api/v1/instances/nginx-fixture/settings', (route) =>
    route.fulfill({ json: { state: 'active', editable: true, associated: [] } }),
  )
  await page.route('**/api/v1/instances/nginx-fixture/logs?*', (route) => {
    const url = new URL(route.request().url())
    requests.push(url)
    const archived = !!url.searchParams.get('archive')
    return route.fulfill({
      json: {
        content: archived
          ? '2026/11/17 12:00:00 [error] archived record'
          : '2026/11/17 12:00:00 [error] current record',
        revision: archived ? 'archive-revision' : 'current-revision',
        size: 100,
        archives: [{ name: archive, size: 100, createdAt: '2026-11-17T12:00:00Z' }],
        canRotate: !archived,
        archive: url.searchParams.get('archive') || '',
        timezone: 'UTC',
        count: 1,
      },
    })
  })
  await page.route('**/api/v1/resources/nginx-fixture/actions/*', (route) => {
    mutation = { action: route.request().url().split('/').pop(), ...route.request().postDataJSON() }
    return route.fulfill({ json: { id: 'log-archive-task', state: 'queued' }, status: 202 })
  })
  await page.goto('/software')
  await page
    .locator('.software-list .el-table__body tr')
    .filter({ hasText: 'Nginx' })
    .getByRole('button', { name: '设置', exact: true })
    .click()
  const dialog = page.locator('.software-settings')
  await dialog.locator('aside').getByRole('button', { name: '错误日志', exact: true }).click()
  await expect(dialog.locator('.service-log')).toContainText('current record')
  await dialog.getByPlaceholder('搜索日志').fill('current')
  const range = dialog.getByRole('combobox', { name: '日志时间范围', exact: true })
  await range.nth(0).fill('2026-11-17 00:00:00')
  await range.nth(1).fill('2026-11-18 00:00:00')
  await range.nth(1).press('Tab')
  await range.nth(0).click()
  await page
    .locator('.el-picker-panel:visible')
    .getByRole('button', { name: '确定', exact: true })
    .click()
  await expect(page.locator('.el-picker-panel:visible')).toHaveCount(0)
  await dialog
    .locator('.el-select')
    .filter({ has: page.getByRole('combobox', { name: '日志级别', exact: true }) })
    .click()
  await page.getByRole('option', { name: '错误', exact: true }).click()
  const filteredLogs = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return url.pathname.endsWith('/nginx-fixture/logs') && url.searchParams.get('level') === 'error'
  })
  await dialog.getByRole('button', { name: '读取', exact: true }).click()
  await filteredLogs
  await expect(dialog.locator('.el-loading-mask')).toBeHidden()
  await expect.poll(() => requests.at(-1)?.searchParams.get('level')).toBe('error')
  expect(requests.at(-1)?.searchParams.get('search')).toBe('current')
  expect(requests.at(-1)?.searchParams.get('since')).toMatch(/^2026-11-1[67]T.*Z$/)
  expect(requests.at(-1)?.searchParams.get('until')).toMatch(/^2026-11-1[78]T.*Z$/)
  const downloaded = page.waitForEvent('download')
  await dialog.getByRole('button', { name: '下载当前结果', exact: true }).click()
  expect((await downloaded).suggestedFilename()).toBe('nginx-error.log')
  await test.step('打开当前日志归档确认', async () => {
    const archiveButton = dialog.getByRole('button', { name: '归档并清理', exact: true })
    await expect(archiveButton).toBeEnabled()
    await archiveButton.click({ timeout: 10000 })
  })
  await expect(page.locator('.el-message-box')).toContainText('其他软件须先停止对应服务')
  await page.locator('.el-message-box').getByRole('button', { name: '取消', exact: true }).click()
  expect(mutation).toBeUndefined()
  await dialog.getByRole('button', { name: '归档并清理', exact: true }).click()
  await page
    .locator('.el-message-box')
    .getByRole('button', { name: '确认操作', exact: true })
    .click()
  await expect.poll(() => mutation?.action).toBe('rotate-log')
  expect(mutation).toMatchObject({
    kind: 'error',
    confirmed: true,
    baseRevision: 'current-revision',
    archive: '',
    siteId: '',
  })
  await dialog
    .locator('.el-select')
    .filter({ has: page.getByRole('combobox', { name: '日志归档', exact: true }) })
    .click()
  await page.getByRole('option').filter({ hasText: archive }).click()
  await expect(dialog.locator('.service-log')).toContainText('archived record')
  await expect(dialog.getByRole('checkbox', { name: '每 5 秒刷新' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: '归档并清理', exact: true })).toBeHidden()
  await dialog.getByRole('button', { name: '删除归档', exact: true }).click()
  await page
    .locator('.el-message-box')
    .getByRole('button', { name: '确认操作', exact: true })
    .click()
  await expect.poll(() => mutation?.action).toBe('delete-log-archive')
  expect(mutation).toMatchObject({ archive, baseRevision: 'archive-revision', confirmed: true })
  await expect(page.locator('.el-message-box')).toBeHidden()
  await expect(dialog.locator('.service-log')).toContainText('current record')
  await page.screenshot({ path: '../.opsview/screenshots/software-logs.png', fullPage: true })
})

test('PHP Session memcached settings preserve masked credentials and confirm scope', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  const php = {
    ...nginxFixture,
    id: 'php-session-fixture',
    name: 'php-main',
    config: { software: 'php', version: '8.4.27', mode: 'native' },
  }
  const site = {
    ...nginxFixture,
    id: 'php-session-site',
    name: 'private-php-site',
    kind: 'site',
    config: { phpInstanceId: php.id },
  }
  let mutation: any
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [php, site] }))
  await page.route(`**/api/v1/instances/${php.id}/settings`, (route) =>
    route.fulfill({ json: { state: 'active', editable: true, associated: [site] } }),
  )
  await page.route(`**/api/v1/instances/${php.id}/session?*`, (route) => {
    const scoped = new URL(route.request().url()).searchParams.get('siteId') === site.id
    return route.fulfill({
      json: {
        handler: 'memcached',
        revision: scoped ? 'site-session-revision' : 'default-session-revision',
        expiredFiles: 0,
        namespace: scoped ? 'ov.php-session-site.' : 'ov.php-session-fixture.',
        memcached: {
          port: 11212,
          saslEnabled: true,
          saslUsername: 'cache-user',
          saslPassword: '••••••••',
        },
      },
    })
  })
  await page.route(`**/api/v1/resources/${php.id}/actions/session`, (route) => {
    mutation = route.request().postDataJSON()
    return route.fulfill({ status: 202, json: { id: 'session-change-task' } })
  })
  await page.goto('/software')
  await page
    .locator('.software-list .el-table__body tr')
    .filter({ hasText: 'PHP' })
    .getByRole('button', { name: '设置', exact: true })
    .click()
  const dialog = page.locator('.software-settings')
  await dialog.locator('aside').getByRole('button', { name: 'Session 配置', exact: true }).click()
  await expect(dialog.getByRole('spinbutton', { name: 'memcached 端口' })).toHaveValue('11212')
  await expect(dialog.getByRole('textbox', { name: 'memcached SASL 用户名' })).toHaveValue(
    'cache-user',
  )
  await expect(dialog.getByRole('textbox', { name: 'memcached SASL 密码' })).toHaveValue('••••••••')
  await dialog.locator('.settings-toolbar .el-select').click()
  await page.getByRole('option', { name: site.name, exact: true }).click()
  await expect(dialog).toContainText('ov.php-session-site.')
  await dialog.getByRole('button', { name: '验证连接并保存', exact: true }).click()
  await page.locator('.el-message-box').getByRole('button', { name: '取消', exact: true }).click()
  expect(mutation).toBeUndefined()
  await dialog.getByRole('button', { name: '验证连接并保存', exact: true }).click()
  await page
    .locator('.el-message-box')
    .getByRole('button', { name: '确认保存', exact: true })
    .click()
  await expect.poll(() => mutation?.handler).toBe('memcached')
  expect(mutation).toMatchObject({
    operation: 'save',
    siteId: site.id,
    baseRevision: 'site-session-revision',
    memcachedPort: 11212,
    saslEnabled: true,
    saslUsername: 'cache-user',
    saslPassword: '••••••••',
    confirmed: true,
  })
  await expect(page.locator('.el-message-box')).toBeHidden()
  const bounds = await dialog.boundingBox()
  expect(bounds!.y).toBeGreaterThanOrEqual(0)
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(1000)
  await page.screenshot({
    path: '../.opsview/screenshots/php-session-memcached.png',
    fullPage: true,
  })
})

test('Docker engine states gate resource requests and negotiate older engines', async ({
  page,
}) => {
  let state = 'not_installed',
    resources = 0
  await page.route('**/api/v1/docker/status', (route) =>
    route.fulfill({
      json: {
        status: state,
        reason: state === 'ready' ? 'Docker 引擎可用' : '尚未安装 Docker 引擎',
        engineVersion: '20.10.24',
        apiVersion: '1.41',
        composeAvailable: true,
        composeVersion: '2.39.0',
      },
    }),
  )
  await page.route('**/api/v1/docker/containers', (route) => {
    resources++
    return route.fulfill({ json: { items: [] } })
  })
  await page.goto('/docker')
  await expect(page.getByRole('button', { name: '安装 Docker 引擎', exact: true })).toBeVisible()
  expect(resources).toBe(0)
  state = 'ready'
  await page.getByRole('button', { name: '重新检测', exact: true }).click()
  await expect(page.getByText('API 1.41', { exact: false })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '重新检测', exact: true })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: '容器', exact: true })).toBeVisible()
  expect(resources).toBe(1)
  await page.getByRole('tab', { name: '应用模板', exact: true }).click()
  await expect(page.locator('.list-panel .el-table')).toBeVisible()
})

test('application entry retires to other websites and plugins use row lists', async ({ page }) => {
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture] }))
  await page.goto('/apps')
  await expect(page).toHaveURL('/sites?tab=other&retired=apps')
  await expect(page.getByRole('tab', { name: '其他项目', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.locator('.el-alert')).toContainText('应用进程功能已下线')
  await expect(page.locator('.sidebar a[href="/apps"]')).toHaveCount(0)
  await page.goto('/software')
  await page.getByRole('tab', { name: '插件市场', exact: true }).click()
  await expect(page.locator('.list-panel .el-table')).toBeVisible()
  await expect(page.locator('.plugin-card')).toHaveCount(0)
  await page.getByRole('tab', { name: '仓库管理', exact: true }).click()
  await expect(
    page.getByRole('button', { name: '添加插件仓库', exact: true }).first(),
  ).toBeVisible()
})

test('server file page navigates root, shows revisions, recovers conflicts and stays within mobile viewport', async ({
  page,
}) => {
  let saved: any,
    current = '/',
    contents = 'hello\n',
    rev = 'revision-1'
  const row = {
    name: '.env',
    path: '/var/www/.env',
    directory: false,
    symlink: false,
    size: 6,
    mode: '0640',
    owner: 'www-data',
    group: 'www-data',
    modifiedAt: new Date().toISOString(),
    revision: rev,
  }
  await page.route('**/api/v1/files', async (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'metadata')
      return route.fulfill({ json: { favorites: [], shortcuts: [{ name: '根目录', path: '/' }] } })
    if (p.action === 'list') {
      current = p.path
      return route.fulfill({
        json: {
          path: current,
          total: 1,
          items: current === '/' ? [{ ...row, name: 'var', path: '/var', directory: true }] : [row],
          readOnly: false,
        },
      })
    }
    if (p.action === 'read') return route.fulfill({ json: { text: contents, revision: rev } })
    if (p.action === 'write') {
      saved = p
      return route.fulfill({
        status: 409,
        json: {
          error: '文件已变化，请重新载入',
          code: 'revision_conflict',
          revision: 'revision-2',
        },
      })
    }
    if (p.action === 'trash') return route.fulfill({ json: { items: [] } })
    return route.fulfill({ json: {} })
  })
  await page.goto('/files')
  await expect(page.getByRole('textbox', { name: '服务器路径', exact: true })).toHaveValue('/')
  await page.getByRole('textbox', { name: '服务器路径', exact: true }).fill('/var/www')
  await page.getByRole('button', { name: '前往', exact: true }).click()
  await expect(page.getByRole('button', { name: '.env', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '编辑', exact: true }).click()
  await expect(page.locator('.el-dialog .cm-content')).toContainText('hello')
  await page.locator('.el-dialog .cm-content').fill('changed')
  await page.getByRole('button', { name: '查看差异并保存', exact: true }).click()
  await expect(page.locator('.el-dialog .log-box')).toContainText('+ 1: changed')
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect.poll(() => saved?.revision).toBe('revision-1')
  await expect(page.locator('.el-message')).toContainText('文件已变化')
  rev = 'revision-2'
  contents = 'external edit'
  await page.getByRole('button', { name: '返回编辑', exact: true }).click()
  await page.getByRole('button', { name: '重新载入', exact: true }).click()
  await expect(page.locator('.el-dialog .cm-content')).toContainText('external edit')
  await page.getByRole('button', { name: '取消', exact: true }).last().click()
  await expect(page.locator('.el-dialog:visible')).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.locator('.sidebar')).not.toBeInViewport()
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)).toBe(
    false,
  )
  await page.locator('.el-table__body-wrapper .el-scrollbar__wrap').evaluate((el) => {
    el.scrollLeft = 0
  })
  await expect(page.getByRole('button', { name: '.env', exact: true })).toBeInViewport()
  await expect(page.locator('.el-table__body td.el-table-fixed-column--right').first()).toHaveCSS(
    'position',
    'static',
  )
  await page.screenshot({ path: '../.opsview/screenshots/files-mobile.png', fullPage: true })
})

test('compact shell preserves account access, tabs and per-list filters across navigation', async ({
  page,
}) => {
  await expect(page.locator('.topbar')).toHaveCount(1)
  await expect(page.locator('.page-heading, .page-footer, .welcome-card, .breadcrumb')).toHaveCount(
    0,
  )
  await expect(page.getByRole('button', { name: '刷新状态' })).toHaveCount(0)
  await page.locator('.sidebar a[href="/software"]').click()
  await page.getByRole('tab', { name: '仓库管理', exact: true }).click()
  await page.getByPlaceholder('搜索名称').fill('keep-filter')
  await page.locator('.sidebar a[href="/databases"]').click()
  await expect(page.locator('.topbar')).toHaveCount(0)
  await page.locator('.sidebar a[href="/software"]').click()
  await expect(page.getByRole('tab', { name: '仓库管理', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.getByPlaceholder('搜索名称')).toHaveValue('keep-filter')
  await page.locator('.sidebar-account button').hover()
  await page.getByRole('menuitem', { name: '退出登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '进入控制台' })).toBeVisible()
})

test('slow independent reads never block navigation and warm switches meet latency budget', async ({
  page,
}) => {
  let overviewReads = 0,
    catalogReads = 0
  await page.route('**/api/v1/overview', async (route) => {
    overviewReads++
    await new Promise((resolve) => setTimeout(resolve, 2000))
    await route.fulfill({ json: { cpu: 1, host: { hostname: 'slow-host' } } }).catch(() => {})
  })
  await page.route('**/api/v1/catalog', async (route) => {
    catalogReads++
    await route.continue()
  })
  await page.reload()
  await expect(page.getByRole('main', { name: '概览', exact: true })).toBeVisible()
  await expect(page.locator('.page-skeleton')).toBeVisible()
  await page.locator('.sidebar a[href="/software"]').click()
  await expect(page.getByRole('tab', { name: '核心软件', exact: true })).toBeVisible()
  await expect(page.locator('.software-list .el-table__body tr').first()).toBeVisible()
  await page.locator('.sidebar a[href="/databases"]').click()
  await expect(page.getByRole('main', { name: '数据库', exact: true })).toBeVisible()
  // Browser-side timers exclude Playwright's click/actionability and assertion polling overhead.
  const timings = await page.evaluate(async () => {
    const samples: number[] = []
    for (let i = 0; i < 20; i++) {
      const target = i % 2 === 0 ? '/software' : '/databases'
      const label = i % 2 === 0 ? '软件与插件' : '数据库'
      const start = performance.now()
      ;(document.querySelector(`.sidebar a[href="${target}"]`) as HTMLElement).click()
      await new Promise<void>((resolve, reject) => {
        const check = () => {
          const main = document.querySelector('main')
          if (
            main?.getAttribute('aria-label') === label &&
            main.querySelector(target === '/software' ? '.software-list .el-table' : '.surface')
          )
            requestAnimationFrame(() => resolve())
          else if (performance.now() - start > 3000) reject(new Error('navigation stalled'))
          else requestAnimationFrame(check)
        }
        check()
      })
      samples.push(performance.now() - start)
    }
    return samples.sort((a, b) => a - b)
  })
  console.log(`Cached navigation P95: ${timings[18]!.toFixed(1)}ms`)
  expect(timings[18]).toBeLessThanOrEqual(300)
  expect(catalogReads).toBe(1)
  expect(overviewReads).toBe(1)
})

test('system loads only selected tab and discards late reads after navigation', async ({
  page,
}) => {
  let audits = 0
  await page.route('**/api/v1/audit', async (route) => {
    audits++
    await route.fulfill({ json: [] })
  })
  await page.route('**/api/v1/system', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 2000))
    await route.fulfill({ json: { portItems: [], serviceItems: [] } }).catch(() => {})
  })
  const feedback = await page.evaluate(async () => {
    const start = performance.now()
    ;(document.querySelector('.sidebar a[href="/system"]') as HTMLElement).click()
    await new Promise<void>((resolve, reject) => {
      const check = () => {
        if (
          document.querySelector('.navigation-progress') ||
          document.querySelector('main[aria-label="系统与安全"]')
        )
          resolve()
        else if (performance.now() - start > 1000) reject(new Error('no navigation feedback'))
        else requestAnimationFrame(check)
      }
      requestAnimationFrame(check)
    })
    return performance.now() - start
  })
  expect(feedback).toBeLessThanOrEqual(200)
  await expect(page.getByRole('tab', { name: '防火墙规则', exact: true })).toBeVisible()
  expect(audits).toBe(0)
  await page.getByRole('tab', { name: '操作审计', exact: true }).click()
  await expect.poll(() => audits).toBe(1)
  await page.locator('.sidebar a[href="/tasks"]').click()
  await expect(page.getByRole('main', { name: '任务中心', exact: true })).toBeVisible()
  await expect(page.getByRole('main')).not.toContainText('AbortError')
})

test('inactive browser polling pauses and failed overview does not discard resources', async ({
  page,
}) => {
  let resources = 0
  await page.route('**/api/v1/resources', async (route) => {
    resources++
    await route.fulfill({ json: [nginxFixture] })
  })
  await page.route('**/api/v1/overview', (route) =>
    route.fulfill({ status: 503, json: { error: '指标暂时不可用' } }),
  )
  await page.reload()
  await expect(page.getByRole('alert')).toContainText('指标暂时不可用')
  await page.locator('.sidebar a[href="/sites"]').click()
  await expect(page.getByRole('button', { name: '添加PHP 项目', exact: false })).toBeVisible()
  const before = resources
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(
      new CustomEvent('opsview:preferences', { detail: { refreshSeconds: 0.1 } }),
    )
  })
  await page.waitForTimeout(350)
  expect(resources).toBe(before)
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new CustomEvent('opsview:preferences', { detail: { refreshSeconds: 15 } }))
  })
  await expect.poll(() => resources).toBeGreaterThan(before)
})

test('file shortcuts remove protected legacy history before displaying it', async ({ page }) => {
  await page.evaluate(() =>
    localStorage.setItem('opsview:file-recent', JSON.stringify(['/proc', '/etc', '/run/opsview'])),
  )
  await page.route('**/api/v1/files', (route) => {
    const body = route.request().postDataJSON()
    if (body.action === 'metadata') {
      expect(body.recentPaths).toContain('/proc')
      return route.fulfill({
        json: {
          favorites: ['/etc'],
          recentPaths: ['/etc'],
          shortcuts: [{ name: '根目录', path: '/' }],
        },
      })
    }
    return route.fulfill({ json: { path: '/', items: [], total: 0, readOnly: false } })
  })
  await page.locator('.sidebar a[href="/files"]').click()
  await page.getByRole('button', { name: '快捷目录', exact: true }).hover()
  await expect(page.getByRole('menuitem', { name: '最近 /etc', exact: true })).toBeVisible()
  await expect(page.getByText('/proc', { exact: false })).toHaveCount(0)
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('opsview:file-recent')))
    .not.toContain('/proc')
})

test('a failed route chunk offers recovery instead of an inert menu', async ({ page }) => {
  let fail = true
  await page.route(/\/assets\/Backups-[^/]+\.js$/, (route) =>
    fail ? route.fulfill({ status: 503, body: 'unavailable' }) : route.continue(),
  )
  await page.locator('.sidebar a[href="/backups"]').click()
  await expect(page.getByRole('alert')).toContainText('页面资源加载失败')
  fail = false
  await page.getByRole('button', { name: '重新加载页面', exact: true }).click()
  await expect(page.getByRole('main', { name: '备份与计划', exact: true })).toBeVisible()
  await expect(page.getByRole('tab', { name: '备份任务', exact: true })).toBeVisible()
})

test('database tabs keep distinct compact tables and independent Redis browsing', async ({
  page,
}) => {
  const instances = ['mysql', 'postgresql', 'redis'].map((software, index) => ({
    id: 'db-instance-' + software,
    kind: 'instance',
    name: software + '-test',
    status: 'ready',
    revision: 1,
    createdAt: '',
    updatedAt: '',
    config: { software, mode: 'native', version: 'test', port: 3306 + index },
  }))
  await page.route('**/api/v1/resources', (route) =>
    route.request().method() === 'GET' ? route.fulfill({ json: instances }) : route.continue(),
  )
  await page.route('**/api/v1/databases?*', (route) => {
    const url = new URL(route.request().url())
    const engine = url.searchParams.get('engine')
    return route.fulfill({
      json: {
        rows: [
          {
            id: 'db-' + engine,
            kind: 'database',
            name: 'example_' + engine,
            status: 'ready',
            revision: 1,
            config: {
              username: 'appuser',
              owner: 'appowner',
              instanceId: 'db-instance-' + engine,
              instanceName: engine + '-test',
              encoding: engine === 'mysql' ? 'utf8mb4' : 'UTF8',
              hasPassword: true,
            },
          },
        ],
      },
    })
  })
  let written: Record<string, any> | undefined
  let recycleRequest: Record<string, any> | undefined
  await page.route('**/api/v1/databases/*/capabilities', (route) =>
    route.fulfill({ json: { connected: true } }),
  )
  await page.route('**/api/v1/databases/*/credential', (route) =>
    route.fulfill({ json: { password: 'fixture-business-secret', displaySeconds: 30 } }),
  )
  await page.route('**/api/v1/databases/*/recycle', (route) => {
    recycleRequest = route.request().postDataJSON()
    return route.fulfill({ status: 202, json: { id: 'fixture-recycle', state: 'queued' } })
  })
  await page.route('**/api/v1/redis/**', (route) => {
    const op = route.request().url().split('/').at(-1)
    if (op === 'write') {
      written = route.request().postDataJSON()
      return route.fulfill({ json: { ok: true } })
    }
    return route.fulfill({
      json:
        op === 'databases'
          ? { count: 16 }
          : op === 'read'
            ? {
                type: 'string',
                key: 'c2Vzc2lvbg==',
                content: { text: 'current-value', base64: 'Y3VycmVudC12YWx1ZQ==', binary: false },
                editable: true,
                complete: true,
                revision: 'revision-1',
                ttlMillis: 30000,
                cursor: '0',
              }
            : {
                cursor: '0',
                rows: [
                  {
                    key: 'c2Vzc2lvbg==',
                    name: 'session',
                    type: 'string',
                    length: 12,
                    ttlMillis: 30000,
                  },
                ],
              },
    })
  })
  await page.goto('/databases')
  await expect(
    page.getByRole('columnheader', { name: '字符集/排序规则', exact: true }),
  ).toBeVisible()
  await page.getByRole('tab', { name: 'PostgreSQL', exact: true }).click()
  await expect(page.getByRole('columnheader', { name: '所有者', exact: true })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: '编码', exact: true })).toBeVisible()
  await page.getByRole('tab', { name: 'Redis', exact: true }).click()
  await expect(page.getByRole('columnheader', { name: '键名', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: 'session', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '添加数据库', exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: 'MySQL', exact: true }).click()
  await expect(page.getByRole('cell', { name: 'example_mysql', exact: true })).toBeVisible()
  await page.screenshot({ path: '../.opsview/screenshots/database-mysql.png', fullPage: true })
  await page.getByRole('button', { name: '•••••• 查看' }).click()
  const credential = page.getByRole('dialog', { name: '查看业务账号密码 · example_mysql' })
  await credential.getByPlaceholder('当前管理员密码').fill('fixture-password')
  await credential.getByRole('button', { name: '验证并查看' }).click()
  await expect(credential.getByText('fixture-business-secret', { exact: true })).toBeVisible()
  await credential.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.getByText('fixture-business-secret', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '更多数据库操作' }).click()
  await page.getByRole('menuitem', { name: '删除并回收' }).click()
  const recycle = page.getByRole('dialog', { name: '删除并进入回收站 · example_mysql' })
  await expect(recycle.getByRole('button', { name: '确认提交' })).toBeDisabled()
  await recycle.getByText('已停止业务写入，确认删除此数据库', { exact: true }).click()
  await recycle.getByRole('button', { name: '确认提交' }).click()
  await expect(recycle).not.toBeVisible()
  expect(recycleRequest).toMatchObject({ maintenanceConfirmed: true, confirmDataDeletion: true })
  await page.getByRole('tab', { name: 'Redis', exact: true }).click()
  await page.screenshot({ path: '../.opsview/screenshots/database-redis.png', fullPage: true })
  await page.getByRole('button', { name: '管理', exact: true }).click()
  const editor = page.getByRole('dialog', { name: 'Redis 键管理' })
  await expect(editor.getByPlaceholder('单次编辑最多 1 MiB')).toHaveValue('current-value')
  await editor.getByPlaceholder('单次编辑最多 1 MiB').fill('edited-value')
  await editor.getByRole('button', { name: '保存（保留 TTL）' }).click()
  await expect(editor).not.toBeVisible()
  expect(written).toMatchObject({
    operation: 'set',
    revision: 'revision-1',
    value: Buffer.from('edited-value').toString('base64'),
    db: 0,
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('tab', { name: 'Redis', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(392)
})

test('database install guidance selects its software despite saved market tab', async ({
  page,
}) => {
  await page.route('**/api/v1/resources', (route) =>
    route.request().method() === 'GET' ? route.fulfill({ json: [] }) : route.continue(),
  )
  await page.route('**/api/v1/databases?*', (route) => route.fulfill({ json: { rows: [] } }))
  await page.goto('/software')
  await page.getByRole('tab', { name: '插件市场', exact: true }).click()
  await page.getByRole('link', { name: '数据库', exact: true }).click()
  await page.getByRole('tab', { name: 'PostgreSQL', exact: true }).click()
  await page.getByRole('button', { name: '前往软件市场安装' }).click()
  await expect(page.getByRole('tab', { name: '核心软件', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.getByRole('cell', { name: /PostgreSQL/ })).toBeVisible()
  await page.getByRole('button', { name: '显示全部软件' }).click()
  await expect(page.getByRole('cell', { name: /MySQL/ })).toBeVisible()
})

test('site dialogs follow domains, preserve manual roots and submit managed processes', async ({
  page,
}) => {
  let submitted: any
  const php = {
    ...nginxFixture,
    id: 'php-fixture',
    name: 'php-8.4',
    config: { software: 'php', version: '8.4.27', mode: 'native' },
  }
  await page.route('**/api/v1/resources', async (route) => {
    if (route.request().method() === 'GET') return route.fulfill({ json: [nginxFixture, php] })
    submitted = route.request().postDataJSON()
    return route.fulfill({
      json: { ...submitted, id: 'site-create-fixture', revision: 1, status: 'pending' },
    })
  })
  await page.route('**/api/v1/resources/site-create-fixture/actions/*', (route) =>
    route.fulfill({ status: 202, json: { id: 'site-task' } }),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: 'PHP 项目', exact: true }).click()
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog
    .getByRole('textbox', { name: '绑定域名', exact: true })
    .fill('php.example.com\nwww.php.example.com')
  await expect(dialog.getByRole('textbox', { name: '根目录', exact: true })).toHaveValue(
    '/www/wwwroot/php.example.com',
  )
  await expect(dialog.getByPlaceholder('默认使用第一个域名')).toHaveValue('php.example.com')
  await dialog
    .getByRole('textbox', { name: '根目录', exact: true })
    .fill('/www/wwwroot/custom-project')
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('changed.example.com')
  await expect(dialog.getByRole('textbox', { name: '根目录', exact: true })).toHaveValue(
    '/www/wwwroot/custom-project',
  )
  await page.screenshot({
    path: '../.opsview/screenshots/site-create-php.png',
    animations: 'disabled',
    fullPage: true,
  })
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('tab', { name: '其他项目', exact: true }).click()
  await page.getByRole('button', { name: '添加项目', exact: true }).click()
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('app.example.com')
  await dialog.getByPlaceholder('绝对路径，例如 /usr/bin/node').fill('/usr/bin/node')
  await dialog.getByPlaceholder('JSON 数组，例如 ["server.js"]').fill('["server.js"]')
  await expect(dialog.getByRole('textbox', { name: '根目录', exact: true })).toHaveValue(
    '/www/wwwroot/app.example.com',
  )
  await page.screenshot({
    path: '../.opsview/screenshots/site-create-process.png',
    animations: 'disabled',
    fullPage: true,
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(dialog.getByRole('button', { name: '创建并部署', exact: true })).toBeInViewport()
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  await page.screenshot({
    path: '../.opsview/screenshots/site-create-mobile.png',
    animations: 'disabled',
    fullPage: true,
  })
  await dialog.getByRole('button', { name: '创建并部署', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeHidden()
  expect(submitted.config.type).toBe('managed')
  expect(submitted.config.user).toBe('isolated')
  expect(submitted.config.rootDirectory).toBe('/www/wwwroot/app.example.com')
  expect(submitted.config.executable).toBe('/usr/bin/node')
  expect(submitted.config.autoStart).toBe(true)
})

test('website toolbar displays real results and confirms Nginx restart', async ({ page }) => {
  const site = {
    id: 'tool-site',
    kind: 'site',
    name: 'tools.example.com',
    revision: 1,
    status: 'ready',
    config: {
      type: 'static',
      instanceId: nginxFixture.id,
      domains: 'tools.example.com',
      documentRoot: '/www/wwwroot/tools.example.com',
    },
  }
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture, site] }))
  await page.route('**/api/v1/sites/tool-site/traffic', (route) =>
    route.fulfill({ json: { todayBytes: 2048, timezone: 'Asia/Shanghai', hours: {} } }),
  )
  await page.route('**/api/v1/sites/tool-site/security', (route) =>
    route.fulfill({
      json: {
        checkedAt: '2026-11-18T10:00:00Z',
        checks: [{ name: '敏感文件检查', status: 'warning', detail: '发现 .env（文件）' }],
      },
    }),
  )
  let restarts = 0
  await page.route('**/api/v1/resources/nginx-fixture/actions/restart', (route) => {
    restarts++
    return route.fulfill({ status: 202, json: { id: 'restart-task' } })
  })
  await page.goto('/sites')
  await page.getByRole('button', { name: '网站统计', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('2.0 KB')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '漏洞扫描', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('1 项需核对')
  await expect(page.getByRole('dialog')).toContainText('不包含 CVE 数据库')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '网站安全', exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('安全响应头')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Nginx 1.30.6 ▾', exact: true }).click()
  await page.getByText('重启 Nginx', { exact: true }).click()
  await expect(page.getByRole('dialog')).toContainText('影响 1 个关联网站')
  expect(restarts).toBe(0)
  await page.getByRole('button', { name: '确认', exact: true }).click()
  await expect.poll(() => restarts).toBe(1)
})

test('software market filter is a button and installed versions have no duplicate install action', async ({
  page,
}) => {
  const fixture = {
    id: 'php-market-installed',
    kind: 'instance',
    name: 'php-8.4.27',
    status: 'ready',
    revision: 1,
    config: {
      software: 'php',
      version: '8.4.27',
      catalogId: 'php-8.4.27',
      mode: 'native',
      port: 9000,
    },
  }
  const plugin = {
    id: 'market-plugin',
    kind: 'plugin',
    name: 'backup-tool',
    status: 'ready',
    revision: 1,
    config: { pluginId: 'backup-tool', version: '1.0.0' },
  }
  await page.route('**/api/v1/resources', (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: [fixture, plugin] })
      : route.continue(),
  )
  await page.route('**/api/v1/catalog', (route) =>
    route.fulfill({
      json: [
        {
          id: 'php-8.4.27',
          software: 'php',
          version: '8.4.27',
          recommended: true,
          methods: ['binary'],
          availability: { binary: '' },
        },
        {
          id: 'php-8.2.35',
          software: 'php',
          version: '8.2.35',
          methods: ['binary'],
          availability: { binary: '' },
        },
      ],
    }),
  )
  await page.route('**/api/v1/plugins', (route) =>
    route.fulfill({
      json: [
        {
          id: 'backup-tool',
          name: '备份工具',
          version: '1.0.0',
          type: 'operations',
          builtin: true,
          form: [],
        },
      ],
    }),
  )
  await page.goto('/software?software=php')
  await expect(page.getByRole('button', { name: '显示全部软件', exact: true })).toHaveClass(
    /el-button--primary/,
  )
  const row = page.locator('.software-list .el-table__body tr').filter({ hasText: 'PHP' })
  await expect(row).toContainText('已安装')
  await expect(row.getByRole('button', { name: '安装', exact: true })).toHaveCount(0)
  await row.locator('.el-select').click()
  await page.getByRole('option', { name: '8.2.35', exact: true }).click()
  await expect(row.getByRole('button', { name: '安装', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '显示全部软件', exact: true }).click()
  await expect(page).toHaveURL('/software')
  await page.getByRole('tab', { name: '插件市场', exact: true }).click()
  const pluginRow = page.getByRole('row').filter({ hasText: '备份工具' })
  await expect(pluginRow.getByRole('button', { name: '设置', exact: true })).toBeEnabled()
  await expect(pluginRow.getByRole('button', { name: '安装', exact: true })).toHaveCount(0)
  await expect(pluginRow.getByRole('button', { name: '升级', exact: true })).toHaveCount(0)
})
