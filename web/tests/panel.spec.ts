import { test, expect, type Locator, type Page } from '@playwright/test'
import { contrast } from '../src/lib/theme'
import fs from 'node:fs'
import path from 'node:path'
const username = 'e2e-admin',
  password = 'OpsView-e2e-only-Password-2026!'

async function overlayGeometry(overlay: Locator) {
  await overlay.evaluate(async (el) => {
    // MessageBox animates its enclosing overlay, which also moves the dialog.
    const animations = new Set(el.getAnimations({ subtree: true }))
    for (let parent = el.parentElement; parent; parent = parent.parentElement) {
      for (const animation of parent.getAnimations()) animations.add(animation)
    }
    await Promise.all(
      [...animations]
        .filter((animation) => Number.isFinite(animation.effect?.getComputedTiming().endTime))
        .map((animation) => animation.finished.catch(() => {})),
    )
  })
  return overlay.evaluate((el) => {
    const rect = (node: Element) => {
      const { top, bottom, height } = node.getBoundingClientRect()
      return { top, bottom, height }
    }
    return {
      outer: rect(el),
      header: rect(
        el.querySelector('.el-dialog__header, .el-drawer__header, .el-message-box__header')!,
      ),
      footer: rect(
        el.querySelector('.el-dialog__footer, .el-drawer__footer, .el-message-box__btns')!,
      ),
      viewport: innerHeight,
    }
  })
}

function expectFixedOverlay(
  before: Awaited<ReturnType<typeof overlayGeometry>>,
  after: Awaited<ReturnType<typeof overlayGeometry>>,
) {
  expect(after.outer.top).toBeGreaterThanOrEqual(0)
  expect(after.outer.bottom).toBeLessThanOrEqual(after.viewport)
  for (const key of ['outer', 'header', 'footer'] as const) {
    expect(Math.abs(after[key].top - before[key].top)).toBeLessThanOrEqual(1)
    expect(Math.abs(after[key].height - before[key].height)).toBeLessThanOrEqual(1)
  }
}
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
    ['/software', '插件市场'],
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
  const dialog = page.getByRole('dialog')
  const geometry = await overlayGeometry(dialog)
  await page.getByPlaceholder('默认使用第一个域名').fill('e2e-invalid-site')
  await page
    .getByRole('textbox', { name: '绑定域名', exact: true })
    .fill('example.com;include /etc/passwd')
  await page.getByRole('button', { name: '创建', exact: true }).click()
  await expect(
    dialog.getByRole('alert').filter({ hasText: '请填写域名以生成项目文件夹' }),
  ).toBeVisible()
  await expect(page.locator('.el-dialog')).toBeVisible()
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeInViewport()
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
  const drawer = page.getByRole('dialog', { name: '任务执行详情', exact: true })
  await expect(drawer.getByRole('button', { name: '关闭', exact: true })).toBeInViewport()
  await drawer.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(drawer).toBeHidden()
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
  await expect(page.getByRole('button', { name: '立即备份面板', exact: true })).toBeVisible()
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
      [
        '',
        '网站名',
        '状态',
        '备份',
        '根目录',
        '日流量',
        '到期时间',
        '备注',
        'PHP',
        'SSL证书',
        '操作',
      ],
    ],
    ['反向代理', ['', '域名', '状态', '代理地址', '日流量', '备注', 'SSL证书', '操作']],
    [
      '其他项目',
      ['', '项目名称', '状态', '运行端口', '根目录', '到期时间', '备注', 'SSL证书', '操作'],
    ],
  ] as const) {
    await page.getByRole('tab', { name: tab, exact: true }).click()
    await expect(page.locator('.site-table .el-table__header th .cell')).toHaveText([...headers])
  }
  await page.screenshot({ path: '../.opsview/screenshots/sites-tabs.png', fullPage: true })
})
test('proxy settings merge paths into the same fixed dialog', async ({ page }) => {
  let submitted: any
  const proxy = {
    id: 'proxy-fixture',
    kind: 'site',
    name: 'proxy.example.com',
    status: 'ready',
    revision: 1,
    config: {
      type: 'proxy',
      domains: 'proxy.example.com',
      instanceId: nginxFixture.id,
      upstream: 'http://127.0.0.1:3000',
    },
  }
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture, proxy] }))
  await page.route('**/api/v1/sites/proxy-fixture/settings', (route) => {
    submitted = route.request().postDataJSON()
    proxy.config = submitted.config
    proxy.revision++
    return route.fulfill({ status: 202, json: { taskId: 'proxy-task' } })
  })
  await page.route('**/api/v1/tasks/proxy-task', (route) =>
    route.fulfill({ json: { id: 'proxy-task', state: 'succeeded' } }),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: '反向代理', exact: true }).click()
  await expect(
    page.locator('.site-table').getByRole('button', { name: '编辑', exact: true }),
  ).toHaveCount(0)
  await page.getByRole('button', { name: 'proxy.example.com', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: '反向代理', exact: true }).click()
  await dialog.getByRole('button', { name: '添加路径规则', exact: true }).click()
  await dialog
    .locator('.proxy-route textarea')
    .first()
    .fill('http://127.0.0.1:3000\nhttp://127.0.0.1:3001')
  await dialog.getByRole('button', { name: '添加路径规则', exact: true }).click()
  const second = dialog.locator('.proxy-route').nth(1)
  await second.getByPlaceholder('/ 或 /api/').fill('/api/')
  await second.locator('textarea').fill('http://127.0.0.1:4000')
  await second.getByPlaceholder('$host 或 api.example.com').fill('api.example.com')
  await dialog.getByRole('button', { name: '保存并应用', exact: true }).click()
  await expect(page.getByText('配置已应用', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', { name: '添加路径规则', exact: true })).toBeVisible()
  await expect(dialog.locator('.proxy-route')).toHaveCount(2)
  expect(submitted.revision).toBe(1)
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
        associated: Array.from({ length: 60 }, (_, index) => ({
          id: `site-${index}`,
          kind: 'site',
          name: `associated-${index}.example.com`,
        })),
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
  await expect(dialog.getByRole('button', { name: '保存守护设置', exact: true })).toBeVisible()
  const geometry = await overlayGeometry(dialog)
  const scroll = dialog.locator('.software-settings-content')
  expect(await scroll.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
  await scroll.evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await dialog.locator('aside').getByRole('button', { name: '配置文件', exact: true }).click()
  await expect(dialog.locator('.cm-editor')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '保存守护设置', exact: true })).toHaveCount(0)
  await expect(
    dialog.locator('.el-dialog__footer').getByRole('button', { name: '校验配置', exact: true }),
  ).toBeVisible()
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await dialog.getByRole('button', { name: '校验配置', exact: true }).click()
  await expect(dialog.locator('.el-alert')).toContainText('外部程序修改')
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await dialog.getByRole('button', { name: '保存并重载', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: '确认配置变更', exact: true })
  await expect(confirmation.getByRole('button', { name: '确认应用', exact: true })).toBeInViewport()
  await confirmation.getByRole('button', { name: '取消', exact: true }).click()
  await expect(confirmation).toBeHidden()
  await expect(dialog.getByRole('button', { name: '保存并重载', exact: true })).toBeFocused()
  await expect(
    dialog.locator('.el-dialog__footer').getByRole('button', { name: '保存并重载', exact: true }),
  ).toBeVisible()
  await page.screenshot({ path: '../.opsview/screenshots/software-settings.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 600 })
  await expect(dialog.getByRole('button', { name: '保存并重启', exact: true })).toBeInViewport()
  const mobileGeometry = await overlayGeometry(dialog)
  await scroll.evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  expectFixedOverlay(mobileGeometry, await overlayGeometry(dialog))
  await page.screenshot({
    path: '../.opsview/screenshots/software-settings-mobile.png',
    fullPage: true,
  })
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(dialog).toBeHidden()
})
test('panel settings have five tabs and stage HTTPS changes inline', async ({ page }) => {
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
    '告警通知',
    '备份恢复',
  ])
  await page.getByRole('tab', { name: '面板设置', exact: true }).click()
  await page
    .locator('.preference-row')
    .filter({ has: page.getByText('面板访问', { exact: true }) })
    .getByRole('button', { name: '修改', exact: true })
    .click()
  await page.getByText('HTTP（关闭 SSL）', { exact: true }).click()
  await page.getByRole('button', { name: '保存并重新连接', exact: true }).click()
  await page.getByRole('button', { name: '确认修改', exact: true }).click()
  await page.getByRole('textbox', { name: '请输入当前管理员密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '验证', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '验证管理员身份', exact: true })).toBeHidden()
  await expect(page.getByRole('link', { name: '打开新地址并确认（120 秒内）' })).toHaveAttribute(
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
  await expect(page.locator('.software-list .el-table')).toBeVisible()
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
  await page.getByRole('button', { name: '编辑服务器路径', exact: true }).click()
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

test('compact file prompts keep validation, keyboard focus and fixed actions on small screens', async ({
  page,
}) => {
  const mutations: string[] = []
  await page.route('**/api/v1/files', (route) => {
    const payload = route.request().postDataJSON()
    if (payload.action === 'metadata')
      return route.fulfill({ json: { favorites: [], shortcuts: [] } })
    if (payload.action === 'list')
      return route.fulfill({ json: { path: '/', items: [], total: 0 } })
    mutations.push(payload.action)
    return route.fulfill({ json: {} })
  })
  await page.goto('/files')
  await page.setViewportSize({ width: 390, height: 500 })
  const trigger = page.getByRole('button', { name: '新建文件', exact: true })
  await trigger.click()
  const prompt = page.locator('.el-message-box')
  const geometry = await overlayGeometry(prompt)
  const input = prompt.getByRole('textbox')
  await expect(input).toBeFocused()
  await input.fill('../escape')
  await prompt.getByRole('button', { name: '创建', exact: true }).click()
  await expect(prompt).toContainText('名称无效')
  expectFixedOverlay(geometry, await overlayGeometry(prompt))
  await expect(prompt.getByRole('button', { name: '取消', exact: true })).toBeInViewport()
  await page.keyboard.press('Escape')
  await expect(prompt).toBeHidden()
  await expect(trigger).toBeFocused()
  expect(mutations).toEqual([])
})

test('file address keeps drafts separate from the open directory and preserves listing on errors', async ({
  page,
}) => {
  const listed: string[] = []
  await page.route('**/api/v1/files', async (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'metadata')
      return route.fulfill({ json: { favorites: ['/var/www'], shortcuts: [] } })
    if (p.action === 'list') {
      listed.push(p.path)
      if (p.path === '/protected')
        return route.fulfill({ status: 403, json: { error: '此路径不开放文件管理' } })
      return route.fulfill({
        json: {
          path: p.path,
          total: 1,
          items: [{ name: 'visible.txt', path: p.path + '/visible.txt', size: 5 }],
          readOnly: false,
        },
      })
    }
    return route.fulfill({ json: {} })
  })
  await page.goto('/files?path=/var/www')
  const crumbs = page.getByRole('navigation', { name: '目录路径' })
  await expect(page.locator('.file-navigation')).toHaveCount(1)
  await expect(crumbs.getByRole('button', { name: 'www', exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '服务器路径', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '编辑服务器路径', exact: true }).focus()
  await page.keyboard.press('Enter')
  const input = page.getByRole('textbox', { name: '服务器路径', exact: true })
  await expect(input).toBeFocused()
  await expect(input).toHaveValue('/var/www')
  await input.fill('/draft/child')
  await input.dispatchEvent('keydown', { key: 'Enter', isComposing: true })
  await expect(input).toBeVisible()
  expect(listed).toEqual(['/var/www'])
  await page.getByRole('button', { name: '搜索本目录', exact: true }).focus()
  await expect(crumbs.getByRole('button', { name: 'child', exact: true })).toBeVisible()
  expect(listed).toEqual(['/var/www'])
  await page.getByRole('button', { name: '刷新目录', exact: true }).click()
  await expect.poll(() => listed.length).toBe(2)
  expect(listed[1]).toBe('/var/www')
  await expect(crumbs.getByRole('button', { name: 'child', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '编辑服务器路径', exact: true }).click()
  await expect(input).toHaveValue('/draft/child')
  await input.fill('/protected')
  await page.getByRole('button', { name: '前往', exact: true }).click()
  await expect(page.locator('.list-error')).toContainText('此路径不开放文件管理')
  await expect(page.getByRole('button', { name: 'visible.txt', exact: true })).toBeVisible()
  await expect(page.locator('.file-path-note')).toContainText('/var/www')
  await page.getByRole('button', { name: '上级', exact: true }).click()
  await expect.poll(() => listed.at(-1)).toBe('/var')
  await expect(crumbs.getByRole('button', { name: 'protected', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: '编辑服务器路径', exact: true }).click()
  await input.fill('/var/www/child')
  await input.press('Enter')
  await expect.poll(() => listed.at(-1)).toBe('/var/www/child')
  await expect(input).toHaveCount(0)
  const before = listed.length
  await crumbs.getByRole('button', { name: 'www', exact: true }).click()
  await expect.poll(() => listed.length).toBe(before + 1)
  await expect(input).toHaveCount(0)
  await expect(page.getByRole('button', { name: '取消收藏', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '编辑服务器路径', exact: true }).click()
  await input.fill('/' + 'very-long-directory/'.repeat(20))
  await input.press('Tab')
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)).toBe(
    false,
  )
})

test('file directory sizes load independently inline, retry and stay attached to paths', async ({
  page,
}) => {
  const ready = new Set<string>(),
    cancelled = new Set<string>(),
    jobs = new Map<string, { path: string; attempt: number }>()
  const attempts: Record<string, number> = {}
  let listCount = 0
  await page.route('**/api/v1/files', async (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'metadata') return route.fulfill({ json: { favorites: [], shortcuts: [] } })
    if (p.action === 'list') {
      listCount++
      const names = p.path === '/' ? ['alpha', 'beta', 'empty'] : ['elsewhere']
      return route.fulfill({
        json: {
          path: p.path,
          total: names.length,
          items: names.map((name) => ({
            name,
            path: (p.path === '/' ? '' : p.path) + '/' + name,
            directory: true,
          })),
          readOnly: false,
        },
      })
    }
    if (p.action === 'size') {
      const attempt = (attempts[p.path] = (attempts[p.path] || 0) + 1)
      const id = 'size-' + jobs.size
      jobs.set(id, { path: p.path, attempt })
      return route.fulfill({ json: { id } })
    }
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/v1/files/tasks/*', async (route) => {
    const id = route.request().url().split('/').at(-1)!
    const job = jobs.get(id)!
    if (cancelled.has(id)) return route.fulfill({ json: { state: 'cancelled' } })
    if (!ready.has(job.path)) return route.fulfill({ json: { state: 'running' } })
    if (job.path === '/beta' && job.attempt === 1)
      return route.fulfill({ json: { state: 'failed', error: '读取目录失败' } })
    const bytes =
      job.path === '/alpha'
        ? job.attempt === 1
          ? 1536
          : 8 * 1024 ** 2
        : job.path === '/beta'
          ? 1024 ** 3 * 1.234
          : job.path === '/'
            ? 1024 ** 4
            : 0
    return route.fulfill({ json: { state: 'succeeded', result: { size: bytes, count: 3 } } })
  })
  await page.route('**/api/v1/tasks/*/cancel', async (route) => {
    cancelled.add(route.request().url().split('/').at(-2)!)
    return route.fulfill({ json: {} })
  })
  await page.goto('/files')
  const alpha = page.getByRole('button', { name: 'alpha大小：计算', exact: true })
  await alpha.click()
  await expect(page.getByRole('button', { name: 'alpha大小：计算中', exact: true })).toBeDisabled()
  await expect(page.locator('.is-loading').first()).toBeVisible()
  await page.getByRole('button', { name: 'beta大小：计算', exact: true }).click()
  await expect.poll(() => jobs.size).toBe(2)
  ready.add('/beta')
  await expect(page.getByRole('button', { name: 'beta大小：重试', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'beta大小：重试', exact: true }).click()
  await expect(page.getByRole('button', { name: 'beta大小：1.23 GB', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'alpha', exact: true }).click()
  ready.add('/alpha')
  await expect(page.getByRole('button', { name: 'elsewhere', exact: true })).toBeVisible()
  await page
    .getByRole('navigation', { name: '目录路径' })
    .getByRole('button', { name: '根目录', exact: true })
    .click()
  await expect(page.getByRole('button', { name: 'alpha大小：1.5 KB', exact: true })).toBeVisible()
  expect(attempts['/alpha']).toBe(1)
  ready.add('/empty')
  await page.getByRole('button', { name: 'empty大小：计算', exact: true }).click()
  await expect(page.getByRole('button', { name: 'empty大小：0 B', exact: true })).toBeVisible()
  await expect(page.locator('.el-dialog:visible')).toHaveCount(0)
  expect(listCount).toBe(3)
  await page.getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '计算大小', exact: true }).click()
  await expect(page.locator('.file-directory-total')).toContainText('计算中')
  ready.add('/')
  await expect(page.locator('.file-directory-total')).toContainText('1 TB')
  await expect(page.locator('.el-dialog:visible')).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: '计算大小', exact: true })).not.toBeVisible()
  await page.screenshot({
    path: '../.opsview/screenshots/files-inline-desktop.png',
    fullPage: true,
    animations: 'disabled',
  })
  await page.locator('.sidebar a[href="/tasks"]').click()
  await page.locator('.sidebar a[href="/files"]').click()
  await expect(page.getByRole('button', { name: 'alpha大小：1.5 KB', exact: true })).toBeVisible()
  await expect(page.locator('.file-directory-total')).toContainText('1 TB')
  await page.getByRole('button', { name: 'alpha大小：1.5 KB', exact: true }).click()
  await expect(page.getByRole('button', { name: 'alpha大小：8 MB', exact: true })).toBeVisible()
  ready.delete('/empty')
  await page.getByRole('button', { name: 'empty大小：0 B', exact: true }).click()
  await page.getByRole('button', { name: '取消计算 empty', exact: true }).click()
  await expect(page.getByRole('button', { name: 'empty大小：重试', exact: true })).toBeVisible()
  ready.delete('/alpha')
  await page.getByRole('button', { name: 'alpha大小：8 MB', exact: true }).click()
  await expect(page.getByRole('button', { name: 'alpha大小：计算中', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'alpha', exact: true }).click()
  await page.getByRole('button', { name: '新建目录', exact: true }).click()
  await page.locator('.el-message-box__input input').fill('new-child')
  await page.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.locator('.el-message-box:visible')).toHaveCount(0)
  ready.add('/alpha')
  await page
    .getByRole('navigation', { name: '目录路径' })
    .getByRole('button', { name: '根目录', exact: true })
    .click()
  await expect(page.getByRole('button', { name: 'alpha大小：计算', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'beta大小：1.23 GB', exact: true })).toBeVisible()
  await expect(page.locator('.file-directory-total')).toHaveCount(0)
})

test('file notes save inline on blur or Enter, cancel, clear and retain failed drafts', async ({
  page,
}) => {
  let note = '',
    listCount = 0,
    fail = false
  const saves: string[] = []
  await page.route('**/api/v1/files', async (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'metadata') return route.fulfill({ json: { favorites: [], shortcuts: [] } })
    if (p.action === 'list') {
      listCount++
      return route.fulfill({
        json: {
          path: '/',
          total: 1,
          items: [{ name: 'file.txt', path: '/file.txt', size: 0, note }],
          readOnly: false,
        },
      })
    }
    if (p.action === 'note') {
      saves.push(p.note)
      await new Promise((resolve) => setTimeout(resolve, 150))
      if (fail) return route.fulfill({ status: 500, json: { error: '备注保存失败' } })
      note = p.note
      return route.fulfill({ json: {} })
    }
    return route.fulfill({ json: {} })
  })
  await page.goto('/files')
  await page.getByRole('button', { name: '添加备注', exact: true }).click()
  const input = page.getByRole('textbox', { name: 'file.txt备注', exact: true })
  await expect(input).toBeFocused()
  await input.fill('first')
  await input.dispatchEvent('keydown', { key: 'Enter', isComposing: true })
  expect(saves).toEqual([])
  await input.press('Enter')
  await page.getByRole('button', { name: '搜索本目录', exact: true }).focus()
  await expect(page.getByRole('button', { name: 'first', exact: true })).toBeVisible()
  expect(saves).toEqual(['first'])
  expect(listCount).toBe(1)
  await page.getByRole('button', { name: 'first', exact: true }).click()
  await input.press('Tab')
  await expect(input).toHaveCount(0)
  expect(saves).toHaveLength(1)
  await page.getByRole('button', { name: 'first', exact: true }).click()
  await input.fill('cancel me')
  await input.press('Escape')
  await expect(page.getByRole('button', { name: 'first', exact: true })).toBeVisible()
  expect(saves).toHaveLength(1)
  await page.getByRole('button', { name: 'first', exact: true }).click()
  await input.fill('blur saves')
  await input.press('Tab')
  await expect(page.getByRole('button', { name: 'blur saves', exact: true })).toBeVisible()
  fail = true
  await page.getByRole('button', { name: 'blur saves', exact: true }).click()
  await input.fill('retained draft')
  await input.press('Enter')
  await expect(
    page.getByRole('button', { name: '重试保存 file.txt备注', exact: true }),
  ).toBeVisible()
  await expect(input).toHaveValue('retained draft')
  fail = false
  await page.getByRole('button', { name: '重试保存 file.txt备注', exact: true }).click()
  await expect(page.getByRole('button', { name: 'retained draft', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'retained draft', exact: true }).click()
  await input.fill('')
  await input.press('Enter')
  await expect(page.getByRole('button', { name: '添加备注', exact: true })).toBeVisible()
  expect(saves).toEqual(['first', 'blur saves', 'retained draft', 'retained draft', ''])
  expect(listCount).toBe(1)
  await expect(page.locator('.el-message-box:visible, .el-dialog:visible')).toHaveCount(0)
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
  await expect(page.locator('.loading-content[aria-busy="true"]')).toBeVisible()
  await page.locator('.sidebar a[href="/software"]').click()
  await expect(page.getByRole('tab', { name: '插件市场', exact: true })).toBeVisible()
  await expect(page.locator('.software-list .el-table__body tr').first()).toBeVisible()
  await page.locator('.sidebar a[href="/databases"]').click()
  await expect(page.getByRole('main', { name: '数据库', exact: true })).toBeVisible()
  // Browser-side timers exclude Playwright's click/actionability and assertion polling overhead.
  const timings = await page.evaluate(async () => {
    const samples: number[] = []
    for (let i = 0; i < 20; i++) {
      const target = i % 2 === 0 ? '/software' : '/databases'
      const label = i % 2 === 0 ? '插件市场' : '数据库'
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
  await expect(page.getByRole('tab', { name: '系统防火墙', exact: true })).toBeVisible()
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
  await expect(page.getByRole('tab', { name: '备份记录', exact: true })).toBeVisible()
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
  await page.getByRole('tab', { name: '仓库管理', exact: true }).click()
  await page.getByRole('link', { name: '数据库', exact: true }).click()
  await page.getByRole('tab', { name: 'PostgreSQL', exact: true }).click()
  await page.getByRole('button', { name: '前往软件市场安装' }).click()
  await expect(page.getByRole('tab', { name: '插件市场', exact: true })).toHaveAttribute(
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
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture, php] }))
  await page.route('**/api/v1/sites', (route) => {
    submitted = route.request().postDataJSON()
    return route.fulfill({
      status: 202,
      json: { siteId: 'site-create-fixture', taskId: 'site-task' },
    })
  })
  await page.route('**/api/v1/files', (route) => route.fulfill({ json: { items: [], total: 0 } }))
  await page.route('**/api/v1/tasks/site-task', (route) =>
    route.fulfill({ json: { id: 'site-task', state: 'succeeded' } }),
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
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeInViewport()
  const geometry = await overlayGeometry(dialog)
  await expect(dialog.getByRole('button', { name: '高级设置', exact: true })).toHaveCount(0)
  const body = dialog.locator('.el-dialog__body')
  expect(await body.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
  await body.evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeInViewport()
  await dialog.getByRole('button', { name: '浏览', exact: true }).click()
  const directory = page.getByRole('dialog', { name: '选择 /www/wwwroot 项目目录', exact: true })
  await expect(directory).toBeVisible()
  await directory.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(directory).toBeHidden()
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeInViewport()
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  await page.screenshot({
    path: '../.opsview/screenshots/site-create-mobile.png',
    animations: 'disabled',
    fullPage: true,
  })
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
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
    name: 'php-8.2.35',
    status: 'ready',
    revision: 1,
    config: {
      software: 'php',
      version: '8.2.35',
      catalogId: 'php-8.2.35',
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
  await page.route('**/api/v1/resources', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    await new Promise((resolve) => setTimeout(resolve, 250))
    return route.fulfill({ json: [fixture, plugin] })
  })
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
  await expect(row.locator('.el-select')).toContainText('8.2.35')
  await expect(row.getByRole('button', { name: '安装', exact: true })).toHaveCount(0)
  await row.locator('.el-select').click()
  await page.getByRole('option', { name: '8.4.27', exact: true }).click()
  await expect(row.getByRole('button', { name: '安装', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '显示全部软件', exact: true }).click()
  await expect(page).toHaveURL('/software')
  await page.getByRole('tab', { name: '插件市场', exact: true }).click()
  const pluginRow = page.getByRole('row').filter({ hasText: '备份工具' })
  await expect(pluginRow.getByRole('button', { name: '设置', exact: true })).toBeEnabled()
  await expect(pluginRow.getByRole('button', { name: '安装', exact: true })).toHaveCount(0)
  await expect(pluginRow.getByRole('button', { name: '升级', exact: true })).toHaveCount(0)
})

test('unified market categories keep every installed version without a separate instance list', async ({
  page,
}) => {
  const php = (id: string, version: string) => ({
    id,
    kind: 'instance',
    name: 'php-' + version,
    status: 'ready',
    revision: 1,
    config: { software: 'php', version, mode: 'native', port: 9000 },
  })
  const backup = {
    id: 'backup-market',
    kind: 'plugin',
    name: 'backup-tool',
    status: 'ready',
    revision: 1,
    config: { pluginId: 'backup-tool', version: '1.0.0' },
  }
  const missing = {
    ...backup,
    id: 'missing-market',
    name: 'local-plugin',
    config: { pluginId: 'local-plugin', version: '2.0.0' },
  }
  await page.route('**/api/v1/resources', (route) =>
    route.fulfill({ json: [php('php-old', '7.4.33'), php('php-new', '8.4.27'), backup, missing] }),
  )
  await page.route('**/api/v1/plugins', (route) =>
    route.fulfill({
      json: [
        {
          id: 'backup-tool',
          name: '备份工具',
          version: '1.0.0',
          type: 'backup',
          builtin: true,
          form: [],
        },
        {
          id: 'docker-tool',
          name: 'Docker工具',
          version: '1.0.0',
          type: 'application',
          form: [],
          permissions: ['docker.compose'],
        },
        {
          id: 'security-tool',
          name: '安全检查',
          version: '1.0.0',
          type: 'operations',
          category: 'security',
          form: [],
        },
        {
          id: 'pro-tool',
          name: '专业扩展',
          version: '1.0.0',
          type: 'operations',
          edition: 'professional',
          form: [],
        },
        {
          id: 'enterprise-tool',
          name: '企业扩展',
          version: '1.0.0',
          type: 'operations',
          edition: 'enterprise',
          form: [],
        },
      ],
    }),
  )
  await page.goto('/software')
  await expect(page.getByRole('tab', { name: '核心软件', exact: true })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '已配置实例', exact: true })).toHaveCount(0)
  await expect(page.getByText('备份工具', { exact: true })).toBeVisible()
  const categories = page.getByRole('group', { name: '应用分类', exact: true })
  await categories.getByRole('button', { name: '已安装', exact: true }).click()
  await expect(page.getByText('local-plugin', { exact: true })).toBeVisible()
  const rows = page.locator('.software-list .el-table__body tr')
  await expect(rows).toHaveCount(4)
  await expect(
    rows.filter({ hasText: 'php-7.4.33' }).getByRole('button', { name: '设置', exact: true }),
  ).toBeEnabled()
  await expect(
    rows.filter({ hasText: 'php-8.4.27' }).getByRole('button', { name: '设置', exact: true }),
  ).toBeEnabled()
  await expect(
    page.locator('.software-list').getByRole('button', { name: '安装', exact: true }),
  ).toHaveCount(0)
  await categories.getByRole('button', { name: 'Docker应用', exact: true }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows).toContainText('Docker工具')
  await categories.getByRole('button', { name: '安全应用', exact: true }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows).toContainText('安全检查')
  await categories.getByRole('button', { name: '专业版应用', exact: true }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows).toContainText('专业扩展')
  await categories.getByRole('button', { name: '企业版应用', exact: true }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows).toContainText('企业扩展')
  await categories.getByRole('button', { name: '运行环境', exact: true }).click()
  await expect(rows).toHaveCount(6)
  await categories.getByRole('button', { name: '免费应用', exact: true }).click()
  await expect(page.getByText('专业扩展', { exact: true })).toHaveCount(0)
  await expect(page.getByText('企业扩展', { exact: true })).toHaveCount(0)
  await expect(page.getByText('备份工具', { exact: true })).toBeVisible()
  await page.getByRole('link', { name: '网站', exact: true }).click()
  await page.getByRole('link', { name: '插件市场', exact: true }).click()
  await expect(categories.getByRole('button', { name: '免费应用', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(categories.getByRole('button', { name: '企业版应用', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(392)
  await page.screenshot({
    path: '../.opsview/screenshots/market-categories-mobile.png',
    animations: 'disabled',
    fullPage: true,
  })
})

const sshSettingsFixture = {
  ports: [555],
  password: false,
  publicKey: true,
  rootLogin: 'prohibit-password',
  maxAuthTries: 6,
  loginGraceTime: 120,
  clientAliveInterval: 0,
  clientAliveCountMax: 3,
}
test('SSH management loads lazily and stages connection settings with explicit confirmation', async ({
  page,
}) => {
  let reads = 0,
    logReads = 0,
    applied = false,
    confirmed = false
  await page.route('**/api/v1/system/firewall', (r) =>
    r.fulfill({
      json: {
        installed: true,
        enabled: true,
        compatible: true,
        rules: [],
        protectedPorts: [555, 18790],
      },
    }),
  )
  await page.route('**/api/v1/system/ssh', (r) => {
    reads++
    return r.fulfill({
      json: {
        available: true,
        editable: true,
        unit: 'ssh.service',
        settings: sshSettingsFixture,
        revision: 'ssh-revision-1',
        keys: { editable: true, rows: [], revision: 'keys-1' },
        pending:
          applied && !confirmed
            ? { deadline: Math.floor(Date.now() / 1000) + 120, ports: [2222] }
            : {},
        matchWarning: '保留当前 SSH 连接，测试新配置',
      },
    })
  })
  await page.route('**/api/v1/system/ssh/logs?*', (r) => {
    logReads++
    return r.fulfill({
      json: {
        rows: [{ status: '成功', detail: 'Accepted publickey for root from 192.0.2.10' }],
        notice: '最近 24 小时最多 200 行',
      },
    })
  })
  await page.route('**/api/v1/system/security/ssh-apply', async (r) => {
    const body = r.request().postDataJSON()
    expect(body.settings.ports).toEqual([2222])
    expect(body.revision).toBe('ssh-revision-1')
    expect(body.confirmedImpact).toBe(true)
    expect(body.source).toBe('192.0.2.10/32')
    applied = true
    await r.fulfill({ status: 202, json: { id: 'ssh-task', state: 'queued' } })
  })
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({ json: [{ id: 'ssh-task', state: 'succeeded', operation: 'system.ssh-apply' }] }),
  )
  await page.route('**/api/v1/system/security/ssh-confirm', async (r) => {
    expect(r.request().postDataJSON().connected).toBe(true)
    confirmed = true
    await r.fulfill({ json: { confirmed: true } })
  })
  await page.goto('/system')
  await expect(page.getByRole('tab', { name: 'SSH 管理', exact: true })).toBeVisible()
  expect(reads).toBe(0)
  await page.getByRole('tab', { name: 'SSH 管理', exact: true }).click()
  await expect(page.getByText('555', { exact: true })).toBeVisible()
  expect(logReads).toBe(0)
  await page.getByRole('button', { name: '修改连接设置' }).click()
  const dialog = page.getByRole('dialog', { name: 'SSH 连接设置' })
  await expect(dialog.getByRole('button', { name: '校验并应用' })).toBeDisabled()
  await dialog.getByPlaceholder('例如 555, 2222').fill('2222')
  await dialog.getByPlaceholder('SSH 客户端 IP/CIDR，UFW 已启用时必填').fill('192.0.2.10/32')
  await dialog.getByText('已保留现有 SSH 会话，并确认云安全组与认证影响', { exact: true }).click()
  await dialog.getByRole('button', { name: '校验并应用' }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByText('修改待确认', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '已重新 SSH 登录，确认保留' }).click()
  await page.getByRole('button', { name: '已用新配置登录', exact: true }).click()
  await expect.poll(() => confirmed).toBe(true)
  await page.getByText('登录记录', { exact: true }).click()
  await expect(page.getByRole('radio', { name: '登录记录', exact: true })).toBeChecked()
  await expect(
    page.getByText('Accepted publickey for root from 192.0.2.10', { exact: true }),
  ).toBeVisible()
  expect(logReads).toBe(1)
  await page.screenshot({ path: '../.opsview/screenshots/system-ssh-logins.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(392)
})

test('firewall inherits system rules directly and disables incompatible mutations', async ({
  page,
}) => {
  await page.route('**/api/v1/system/firewall', (r) =>
    r.fulfill({
      json: {
        backend: 'ufw',
        installed: true,
        enabled: true,
        compatible: false,
        reason: '检测到不兼容的自定义防火墙，仅提供诊断',
        rules: [
          {
            id: 'one',
            kind: 'port',
            protocol: 'tcp',
            ports: '555',
            family: 'ipv4',
            direction: 'in',
            action: 'allow',
            source: 'any',
            destination: 'any',
            note: 'ssh',
            editable: true,
          },
          {
            id: 'two',
            kind: 'port',
            protocol: 'tcp',
            ports: '80',
            family: 'ipv6',
            direction: 'in',
            action: 'deny',
            source: '2001:db8::/32',
            destination: 'any',
            note: 'test',
            editable: true,
          },
        ],
        revision: 'one',
        counts: { port: 2, ip: 0, forward: 0, region: 0 },
        protectedPorts: [555, 18790],
      },
    }),
  )
  await page.goto('/system')
  await expect(page.getByRole('switch', { name: '防火墙开关', exact: true })).toBeDisabled()
  await expect(page.getByRole('tab', { name: '端口规则：2', exact: true })).toBeVisible()
  await expect(page.getByRole('cell', { name: '2001:db8::/32', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '修改', exact: true }).first()).toBeDisabled()
  await expect(page.getByRole('button', { name: '检查规则', exact: true })).toHaveCount(0)
  await expect(page.getByText('系统实际规则', { exact: true })).toHaveCount(0)
  await page.screenshot({
    path: '../.opsview/screenshots/system-firewall-actual.png',
    fullPage: true,
  })
})

test('firewall tabs edit inherited rules, preview imports and reconcile selected changes', async ({
  page,
}) => {
  let saved: any, observed: any
  const rule = {
    id: 'inherited',
    kind: 'port',
    protocol: 'tcp',
    ports: '8080',
    family: 'ipv4',
    direction: 'in',
    action: 'allow',
    source: 'any',
    destination: 'any',
    note: '继承的端口',
    editable: true,
    permanent: true,
    runtime: true,
    listening: true,
  }
  const status = {
    backend: 'ufw',
    installed: true,
    enabled: true,
    compatible: true,
    revision: 'revision-1',
    rules: [rule],
    counts: { port: 1, ip: 0, forward: 0, region: 0 },
    protectedPorts: [555, 18790],
    pending: {},
  }
  await page.route('**/api/v1/system/firewall', (r) => r.fulfill({ json: status }))
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({ json: [{ id: 'firewall-task', state: 'succeeded' }] }),
  )
  await page.route('**/api/v1/system/firewall/save', (r) => {
    saved = r.request().postDataJSON()
    return r.fulfill({ status: 202, json: { id: 'firewall-task' } })
  })
  await page.route('**/api/v1/system/firewall/import-preview', (r) =>
    r.fulfill({
      json: {
        token: 'preview-token',
        revision: 'revision-1',
        rows: [
          { index: 0, status: 'ready', rule: { ...rule, ports: '8443' } },
          { index: 1, status: 'unsupported', reason: '不支持动态域名', rule: {} },
        ],
      },
    }),
  )
  await page.route('**/api/v1/system/firewall/import-apply', (r) => {
    expect(r.request().postDataJSON().indices).toEqual([0])
    expect(r.request().postDataJSON().token).toBe('preview-token')
    return r.fulfill({ status: 202, json: { id: 'firewall-task' } })
  })
  await page.route('**/api/v1/system/firewall/sync-preview', (r) =>
    r.fulfill({
      json: {
        revision: 'revision-1',
        rows: [{ rule, change: 'system-added', defaultAction: 'observe' }],
      },
    }),
  )
  await page.route('**/api/v1/system/firewall/sync-observe', (r) => {
    observed = r.request().postDataJSON()
    return r.fulfill({ status: 202, json: { id: 'firewall-task' } })
  })
  await page.goto('/system')
  await expect(page.getByText('继承的端口', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '修改', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '修改端口规则', exact: true })
  await expect(dialog.getByLabel('规则端口')).toHaveValue('8080')
  await dialog.getByLabel('规则端口').fill('8081,8083-8085')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(saved.id).toBe('inherited')
  expect(saved.revision).toBe('revision-1')
  expect(saved.rule.ports).toBe('8081,8083-8085')
  await page.getByRole('tab', { name: 'IP规则：0', exact: true }).click()
  await expect(page.getByRole('button', { name: '添加IP规则', exact: true })).toBeVisible()
  await page.getByRole('tab', { name: '端口转发：0', exact: true }).click()
  await page.getByRole('button', { name: '添加端口转发', exact: true }).click()
  await expect(page.getByPlaceholder('单端口或等长范围')).toBeVisible()
  await page
    .getByRole('dialog', { name: '添加端口转发', exact: true })
    .getByRole('button', { name: '取消', exact: true })
    .click()
  await page.getByRole('tab', { name: '地区规则：0', exact: true }).click()
  await expect(page.getByRole('button', { name: '更新地区数据', exact: true })).toBeVisible()
  await page.getByRole('tab', { name: '端口规则：1', exact: true }).click()
  await page.getByRole('button', { name: '导入规则', exact: true }).click()
  const importer = page.getByRole('dialog', { name: '导入防火墙规则', exact: true })
  await importer.getByPlaceholder('粘贴规则内容').fill('{"rules":[]}')
  await importer.getByRole('button', { name: '预览规则', exact: true }).click()
  await expect(importer.getByText('不支持动态域名', { exact: true })).toBeVisible()
  await importer.getByRole('row').filter({ hasText: '8443' }).locator('label.el-checkbox').click()
  await importer.getByRole('button', { name: '应用所选规则', exact: true }).click()
  await expect(importer).not.toBeVisible()
  await page.getByRole('button', { name: '同步规则', exact: true }).click()
  const sync = page.getByRole('dialog', { name: '同步规则差异', exact: true })
  await sync.getByRole('row').filter({ hasText: '8080' }).locator('label.el-checkbox').click()
  await sync.getByRole('button', { name: '以系统更新记录', exact: true }).click()
  await expect(sync).not.toBeVisible()
  expect(observed.ids).toEqual(['inherited'])
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(392)
})

test('firewall switch updates system state both ways while preserving inherited rules', async ({
  page,
}) => {
  const modes: any[] = []
  const status = {
    backend: 'ufw',
    installed: true,
    enabled: true,
    compatible: true,
    modeAvailable: true,
    warning: '检测到其他软件的入站规则。此处仅管理 UFW，保留外部规则。',
    managementSource: '192.0.2.25',
    revision: 'mode-1',
    pending: {} as any,
    rules: [
      {
        id: 'existing',
        kind: 'port',
        protocol: 'tcp',
        ports: '8080',
        family: 'ipv4',
        direction: 'in',
        action: 'allow',
        source: 'any',
        destination: 'any',
        editable: true,
        permanent: true,
        runtime: true,
      },
    ],
    counts: { port: 1, ip: 0, forward: 0, region: 0 },
    protectedPorts: [555, 18790],
  }
  await page.route('**/api/v1/system/firewall', (r) => r.fulfill({ json: status }))
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({ json: [{ id: 'mode-task', state: 'succeeded' }] }),
  )
  await page.route('**/api/v1/system/security/firewall-mode', (r) => {
    const params = r.request().postDataJSON()
    modes.push(params)
    status.enabled = params.enabled
    status.rules[0]!.runtime = params.enabled
    status.pending = { mode: true, deadline: Math.floor(Date.now() / 1000) + 120 }
    return r.fulfill({ status: 202, json: { id: 'mode-task' } })
  })
  await page.route('**/api/v1/system/security/firewall-confirm', (r) => {
    expect(r.request().postDataJSON().connected).toBe(true)
    status.pending = {}
    return r.fulfill({ json: { confirmed: true } })
  })
  await page.goto('/system')
  const control = page.getByRole('switch', { name: '防火墙开关', exact: true })
  await expect(control).toBeEnabled()
  for (const enabled of [false, true]) {
    await page.locator('.el-switch').filter({ has: control }).click()
    const dialog = page.getByRole('dialog', {
      name: enabled ? '启用防火墙' : '关闭防火墙',
      exact: true,
    })
    if (enabled)
      await expect(dialog.getByPlaceholder('面板和 SSH 客户端 IP/CIDR')).toHaveValue('192.0.2.25')
    await dialog.getByText('已了解影响，并保留现有 SSH 会话', { exact: true }).click()
    await dialog.getByRole('button', { name: '应用并等待确认', exact: true }).click()
    await expect(dialog).not.toBeVisible()
    if (enabled) await expect(control).toBeChecked()
    else await expect(control).not.toBeChecked()
    await expect(page.getByRole('tab', { name: '端口规则：1', exact: true })).toBeVisible()
    await expect(control).toBeDisabled()
    await page.getByRole('button', { name: '访问正常，确认保留', exact: true }).click()
    await page.getByRole('button', { name: '访问正常，保留', exact: true }).click()
    await expect(control).toBeEnabled()
    await expect(page.getByRole('button', { name: '修改', exact: true })).toBeEnabled()
  }
  expect(modes.map((p) => p.enabled)).toEqual([false, true])
  expect(modes.every((p) => p.confirmedImpact && p.source === '192.0.2.25')).toBe(true)
})

test('firewall edits and deletes inherited system ports and exposes separate mode restrictions', async ({
  page,
}) => {
  let saved: any, deleted: any
  const status = {
    backend: 'firewalld',
    installed: true,
    enabled: true,
    compatible: true,
    modeAvailable: false,
    modeReason: '存在外部 iptables 链，firewalld 普通规则仍可修改',
    managementSource: '192.0.2.25',
    revision: 'ports-1',
    pending: {},
    rules: [
      {
        id: 'existing',
        service: '@port:8080/tcp',
        kind: 'port',
        protocol: 'tcp',
        ports: '8080',
        family: 'dual',
        direction: 'in',
        action: 'allow',
        source: 'any',
        destination: 'any',
        editable: true,
        permanent: true,
        runtime: true,
      },
    ],
    counts: { port: 1, ip: 0, forward: 0, region: 0 },
    protectedPorts: [555, 18790],
  }
  await page.route('**/api/v1/system/firewall', (r) => r.fulfill({ json: status }))
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({ json: [{ id: 'port-task', state: 'succeeded' }] }),
  )
  await page.route('**/api/v1/system/firewall/save', (r) => {
    saved = r.request().postDataJSON()
    status.rules = [{ ...status.rules[0], ...saved.rule, id: 'updated' }]
    status.revision = 'ports-2'
    return r.fulfill({ status: 202, json: { id: 'port-task' } })
  })
  await page.route('**/api/v1/system/firewall/delete', (r) => {
    deleted = r.request().postDataJSON()
    status.rules = []
    status.counts.port = 0
    status.revision = 'ports-3'
    return r.fulfill({ status: 202, json: { id: 'port-task' } })
  })
  await page.goto('/system')
  await expect(page.getByRole('switch', { name: '防火墙开关', exact: true })).toBeDisabled()
  await expect(page.getByRole('button', { name: '修改', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '修改', exact: true }).click()
  const editor = page.getByRole('dialog', { name: '修改端口规则', exact: true })
  await editor.getByLabel('规则端口').fill('8081')
  await editor.getByRole('button', { name: '保存', exact: true }).click()
  await expect(editor).not.toBeVisible()
  expect(saved.id).toBe('existing')
  expect(saved.revision).toBe('ports-1')
  expect(saved.rule.ports).toBe('8081')
  await expect(page.getByRole('cell', { name: '8081 双栈', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '删除', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: '删除防火墙规则', exact: true })
  await expect(confirm.getByText('修改同时保存到系统配置。', { exact: false })).toBeVisible()
  await confirm.getByRole('button', { name: '删除', exact: true }).click()
  await expect(confirm).not.toBeVisible()
  expect(deleted.id).toBe('updated')
  expect(deleted.revision).toBe('ports-2')
  await expect(page.getByRole('tab', { name: '端口规则：0', exact: true })).toBeVisible()
})

test('deleting a management port collects source and explicit confirmation in its dialog', async ({
  page,
}) => {
  let deleted: any
  const status = {
    backend: 'ufw',
    installed: true,
    enabled: true,
    compatible: true,
    managementSource: '192.0.2.25',
    revision: 'protected-1',
    pending: {} as any,
    rules: [
      {
        id: 'panel-port',
        kind: 'port',
        protocol: 'tcp',
        ports: '18790',
        family: 'ipv4',
        direction: 'in',
        action: 'allow',
        source: 'any',
        destination: 'any',
        editable: true,
      },
    ],
    counts: { port: 1, ip: 0, forward: 0, region: 0 },
    protectedPorts: [555, 18790],
  }
  await page.route('**/api/v1/system/firewall', (r) => r.fulfill({ json: status }))
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({ json: [{ id: 'protected-task', state: 'succeeded' }] }),
  )
  await page.route('**/api/v1/system/firewall/delete', (r) => {
    deleted = r.request().postDataJSON()
    status.rules = []
    status.pending = { deadline: Math.floor(Date.now() / 1000) + 120 }
    return r.fulfill({ status: 202, json: { id: 'protected-task' } })
  })
  await page.goto('/system')
  await page.getByRole('button', { name: '删除', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: '删除防火墙规则', exact: true })
  await expect(confirm.getByLabel('删除管理来源')).toHaveValue('192.0.2.25')
  await expect(confirm.getByRole('button', { name: '删除', exact: true })).toBeDisabled()
  await confirm.getByText('已确认影响并保留 SSH 会话', { exact: true }).click()
  await confirm.getByRole('button', { name: '删除', exact: true }).click()
  await expect(confirm).not.toBeVisible()
  expect(deleted).toMatchObject({ id: 'panel-port', source: '192.0.2.25', confirmedImpact: true })
  await expect(page.getByRole('button', { name: '访问正常，确认保留', exact: true })).toBeVisible()
})

test('overview omits task shortcut and cold pages keep navigation above local loading', async ({
  page,
}) => {
  await expect(page.locator('.topbar a[href="/tasks"], .topbar .notification')).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'GitHub 项目仓库（新标签页打开）' })).toHaveAttribute(
    'target',
    '_blank',
  )
  await page.route('**/api/v1/catalog', async (r) => {
    await new Promise((resolve) => setTimeout(resolve, 1200))
    await r.fulfill({ json: [] }).catch(() => {})
  })
  await page.route('**/api/v1/plugins', async (r) => {
    await new Promise((resolve) => setTimeout(resolve, 1200))
    await r.fulfill({ json: [] }).catch(() => {})
  })
  await page.goto('/software')
  await expect(page.getByRole('tab', { name: '插件市场', exact: true })).toBeVisible()
  await expect(page.locator('.software-list .loading-content[aria-busy="true"]')).toBeVisible()
  await expect(page.locator('.page-skeleton')).toHaveCount(0)
  const y = await page
    .getByRole('tab', { name: '插件市场', exact: true })
    .evaluate((el) => el.getBoundingClientRect().y)
  await expect(page.locator('.software-list .el-table')).toBeVisible()
  expect(
    Math.abs(
      (await page
        .getByRole('tab', { name: '插件市场', exact: true })
        .evaluate((el) => el.getBoundingClientRect().y)) - y,
    ),
  ).toBeLessThanOrEqual(1)
  await page.route('**/api/v1/docker/status', async (r) => {
    await new Promise((resolve) => setTimeout(resolve, 1200))
    await r.fulfill({ json: { status: 'ready' } }).catch(() => {})
  })
  await page.route('**/api/v1/docker/containers', async (r) => {
    await new Promise((resolve) => setTimeout(resolve, 800))
    await r.fulfill({ json: { items: [] } }).catch(() => {})
  })
  await page.locator('.sidebar a[href="/docker"]').click()
  await expect(page.getByRole('tab', { name: '容器', exact: true })).toBeVisible()
  await expect(page.locator('.loading-content[aria-busy="true"]').first()).toBeVisible()
  await expect(page.locator('.engine-message')).toHaveCount(0)
  const dockerY = await page
    .getByRole('tab', { name: '容器', exact: true })
    .evaluate((el) => el.getBoundingClientRect().y)
  await expect(page.getByText('当前没有资源', { exact: true })).toBeVisible()
  expect(
    Math.abs(
      (await page
        .getByRole('tab', { name: '容器', exact: true })
        .evaluate((el) => el.getBoundingClientRect().y)) - dockerY,
    ),
  ).toBeLessThanOrEqual(1)
})

test('SSH accounts reverify administrator, clear secrets and retain fixed modal actions', async ({
  page,
}) => {
  await page.route('**/api/v1/system/firewall', (r) =>
    r.fulfill({ json: { rules: [], counts: {}, installed: false } }),
  )
  await page.route('**/api/v1/system/ssh', (r) =>
    r.fulfill({
      json: {
        available: true,
        editable: true,
        settings: sshSettingsFixture,
        service: { running: true, controllable: true },
        keys: { rows: [] },
        pending: {},
      },
    }),
  )
  await page.route('**/api/v1/system/ssh/users', (r) =>
    r.fulfill({
      json: {
        rows: [
          {
            username: 'example-user',
            uid: 1000,
            home: '/home/example-user',
            shell: '/bin/bash',
            locked: false,
            revision: 'user-r1',
          },
        ],
      },
    }),
  )
  let changes = 0
  await page.route('**/api/v1/system/security/ssh-user', (r) => {
    const body = r.request().postDataJSON()
    expect(body.currentPassword).toBe('fixture-admin-verification')
    expect(body.password).toBe('fixture-new-system-password')
    expect(body.revision).toBe('user-r1')
    expect(body.confirmedImpact).toBe(true)
    changes++
    return r.fulfill({ status: 409, json: { error: '账号已由外部修改，请重新载入' } })
  })
  await page.goto('/system')
  await page.getByRole('tab', { name: 'SSH 管理', exact: true }).click()
  await page.getByText('系统账号', { exact: true }).click()
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '修改系统账号密码', exact: true })
  const before = await overlayGeometry(dialog)
  await dialog
    .getByRole('textbox', { name: '新系统密码', exact: true })
    .fill('fixture-new-system-password')
  await dialog
    .getByRole('textbox', { name: '重复系统密码', exact: true })
    .fill('fixture-new-system-password')
  await dialog
    .getByRole('textbox', { name: '面板管理员密码', exact: true })
    .fill('fixture-admin-verification')
  await dialog.getByRole('button', { name: '验证并执行' }).click()
  await expect(dialog).toContainText('账号已由外部修改')
  await expect(dialog.getByRole('textbox', { name: '新系统密码', exact: true })).toHaveValue('')
  await expect(dialog.getByRole('textbox', { name: '面板管理员密码', exact: true })).toHaveValue('')
  expectFixedOverlay(before, await overlayGeometry(dialog))
  expect(changes).toBe(1)
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByText('基础设置', { exact: true }).click()
  await page.getByRole('button', { name: '停用 SSH', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'SSH 服务操作' })).toContainText('120 秒')
  await page.getByRole('button', { name: '取消', exact: true }).click()
})

test('schedule rows support paused copies, run attribution and guided cycles', async ({ page }) => {
  let enabled = true,
    executions = 0
  const backup = {
    id: 'backup-schedule-test',
    kind: 'backup',
    name: 'site-backup',
    status: 'ready',
    revision: 1,
    config: { targetId: 'test-site', destination: 'local', retention: 7 },
  }
  const schedule = () => ({
    id: 'schedule-test',
    kind: 'schedule',
    name: 'daily-backup',
    status: 'ready',
    revision: 1,
    config: { targetId: backup.id, operation: 'backup', cron: '0 3 * * *', enabled },
  })
  await page.route('**/api/v1/resources', (r) =>
    r.request().method() === 'GET' ? r.fulfill({ json: [backup, schedule()] }) : r.continue(),
  )
  await page.route('**/api/v1/resources/schedule-test', (r) => {
    enabled = r.request().postDataJSON().config.enabled
    return r.fulfill({ json: schedule() })
  })
  await page.route('**/api/v1/schedules/schedule-test', (r) => r.fulfill({ json: schedule() }))
  await page.route('**/api/v1/schedules/schedule-test/runs', (r) =>
    r.fulfill({
      json: [{ id: 'scheduled-task', state: 'succeeded', createdAt: '2026-11-19T01:00:00Z' }],
    }),
  )
  await page.route('**/api/v1/schedules', (r) =>
    r.fulfill({
      json: {
        timezone: 'Asia/Shanghai',
        zone: 'CST',
        rows: [
          {
            resource: schedule(),
            targetName: 'site-backup',
            nextRun: enabled ? '2026-11-20T03:00:00+08:00' : '',
          },
        ],
      },
    }),
  )
  await page.route('**/api/v1/resources/schedule-test/actions/run', (r) => {
    executions++
    return r.fulfill({ status: 202, json: { id: 'scheduled-task', state: 'queued' } })
  })
  await page.route('**/api/v1/tasks', (r) =>
    r.fulfill({
      json: executions
        ? [
            {
              id: 'scheduled-task',
              resourceId: backup.id,
              scheduleId: 'schedule-test',
              operation: 'backup.backup',
              state: 'succeeded',
              createdAt: '2026-11-19T01:00:00Z',
            },
          ]
        : [],
    }),
  )
  await page.route('**/api/v1/tasks/scheduled-task/events', (r) =>
    r.fulfill({
      json: [{ message: '加密备份及 SHA-256 校验完成', createdAt: '2026-11-19T01:00:00Z' }],
    }),
  )
  await page.goto('/backups')
  await page.getByRole('tab', { name: '计划任务', exact: true }).click()
  await expect(page.getByText('daily-backup', { exact: true })).toBeVisible()
  const filters = page.locator('.page-toolbar .el-select')
  await expect(filters).toHaveCount(3)
  for (const filter of await filters.all()) {
    expect((await filter.boundingBox())!.width).toBeLessThanOrEqual(160)
  }
  await page.screenshot({ path: '../.opsview/screenshots/rc17-schedules.png', fullPage: true })
  await page.getByRole('button', { name: '已启用', exact: true }).click()
  await expect(page.getByRole('button', { name: '已暂停', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '执行', exact: true }).click()
  await expect.poll(() => executions).toBe(1)
  await page.getByRole('button', { name: '日志', exact: true }).click()
  await page.getByRole('button', { name: '查看日志', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '计划执行历史' })).toContainText('SHA-256 校验完成')
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '复制', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '创建计划任务', exact: true })
  await expect(dialog).toBeVisible()
  await overlayGeometry(dialog)
  await page.screenshot({
    path: '../.opsview/screenshots/rc17-schedule-editor.png',
    fullPage: true,
  })
  await expect(dialog.getByRole('switch', { name: '启用', exact: true })).not.toBeChecked()
  await expect(dialog.getByRole('textbox', { name: 'Cron 表达式', exact: true })).toHaveValue(
    '0 3 * * *',
  )
  await dialog.getByRole('combobox', { name: '执行周期', exact: true }).press('ArrowDown')
  await page.getByRole('option', { name: '每隔 N 分钟', exact: true }).click()
  await dialog.getByRole('spinbutton', { name: '间隔分钟', exact: true }).fill('15')
  await dialog.getByRole('spinbutton', { name: '间隔分钟', exact: true }).press('Tab')
  await expect(dialog.getByRole('textbox', { name: 'Cron 表达式', exact: true })).toHaveValue(
    '*/15 * * * *',
  )
})

test('script templates retain revisions and create paused schedules with fixed dialog actions', async ({
  page,
}) => {
  await page.route('**/api/v1/schedules/capabilities', (r) =>
    r.fulfill({ json: { users: ['opsview-task', 'root'], interpreters: ['/usr/bin/python3'] } }),
  )
  await page.goto('/backups')
  await expect(page.getByRole('tab')).toHaveText(['计划任务', '备份记录', '脚本库'])
  await page.getByRole('tab', { name: '脚本库', exact: true }).click()
  await page.getByRole('button', { name: '添加脚本', exact: true }).click()
  let dialog = page.getByRole('dialog', { name: '添加脚本模板', exact: true })
  const geometry = await overlayGeometry(dialog)
  await dialog.getByRole('textbox', { name: '名称', exact: true }).fill('script-lifecycle')
  await dialog
    .getByRole('textbox', { name: '内容', exact: true })
    .fill('echo fixture\n'.repeat(120))
  await dialog.locator('.el-dialog__body').evaluate((el) => (el.scrollTop = el.scrollHeight))
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  const row = page.getByRole('row').filter({ hasText: 'script-lifecycle' })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '编辑', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑脚本模板', exact: true })
  await dialog.getByRole('textbox', { name: '内容', exact: true }).fill('exit 7')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await row.getByRole('button', { name: '编辑', exact: true }).click()
  await dialog.getByRole('combobox', { name: '历史版本', exact: true }).press('ArrowDown')
  await expect(page.getByRole('option')).toHaveCount(2)
  await page.keyboard.press('Escape')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await row.getByRole('button', { name: '创建计划', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '创建计划任务', exact: true })
  await expect(dialog.getByRole('switch', { name: '启用', exact: true })).not.toBeChecked()
  await expect(dialog.getByRole('textbox', { name: '脚本内容', exact: true })).toHaveValue('exit 7')
  await dialog.getByRole('textbox', { name: '任务名称', exact: true }).fill('template-plan')
  await dialog.getByRole('combobox', { name: '执行用户', exact: true }).press('ArrowDown')
  await page.getByRole('option', { name: 'root', exact: true }).click()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '验证管理员身份', exact: true })).toBeVisible()
  await page.locator('.el-message-box').getByRole('button', { name: '取消', exact: true }).click()
  await expect(dialog).toBeVisible()
  await dialog.getByRole('combobox', { name: '执行用户', exact: true }).press('ArrowDown')
  await page.getByRole('option', { name: 'opsview-task', exact: true }).click()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toBeHidden()
  await page.getByRole('tab', { name: '计划任务', exact: true }).click()
  const planned = page.getByRole('row').filter({ hasText: 'template-plan' })
  await expect(planned).toContainText('已暂停')
  await planned.getByRole('button', { name: '删除', exact: true }).click()
  await page.locator('.el-message-box').getByRole('button', { name: '删除', exact: true }).click()
  await expect(planned).toHaveCount(0)
  await page.getByRole('tab', { name: '脚本库', exact: true }).click()
  await row.getByRole('button', { name: '删除', exact: true }).click()
  await page.locator('.el-message-box').getByRole('button', { name: '删除', exact: true }).click()
  await expect(row).toHaveCount(0)
})

test('settings CAS conflicts preserve editor and notification tabs load independently', async ({
  page,
}) => {
  let rules = 0,
    channels = 0,
    records = 0
  await page.route('**/api/v1/notifications/*', (r) => {
    const kind = new URL(r.request().url()).pathname.split('/').at(-1)
    if (kind === 'rules') rules++
    if (kind === 'channels') channels++
    if (kind === 'records') records++
    return r.fulfill({ json: [] })
  })
  await page.goto('/settings')
  let releaseGroup!: () => void
  const groupResponse = new Promise<void>((resolve) => {
    releaseGroup = resolve
  })
  await page.route('**/api/v1/settings/groups/panel', async (route) => {
    await groupResponse
    await route.continue()
  })
  const setting = page
    .locator('.preference-item')
    .filter({ has: page.getByText('默认建站目录', { exact: true }) })
  await setting.getByRole('button', { name: '修改默认建站目录', exact: true }).click()
  await expect(setting.getByRole('textbox', { name: '默认建站目录', exact: true })).toBeDisabled()
  releaseGroup()
  await setting
    .getByRole('textbox', { name: '默认建站目录', exact: true })
    .fill('/www/conflicting-directory')
  await page.route('**/api/v1/settings/groups/panel', (r) =>
    r.request().method() === 'PATCH'
      ? r.fulfill({ status: 409, json: { error: '设置已被修改，请重新载入' } })
      : r.continue(),
  )
  await setting.getByRole('button', { name: '保存', exact: true }).click()
  await expect(setting).toContainText('设置已被修改')
  await expect(setting.getByRole('textbox', { name: '默认建站目录', exact: true })).toHaveValue(
    '/www/conflicting-directory',
  )
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(setting.getByRole('button', { name: '保存', exact: true })).toBeInViewport()
  await setting.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('tab', { name: '告警通知', exact: true }).click()
  await expect.poll(() => rules).toBe(1)
  expect(channels).toBe(0)
  expect(records).toBe(0)
  await page.getByRole('tab', { name: '通知渠道', exact: true }).click()
  await expect.poll(() => channels).toBe(1)
  expect(records).toBe(0)
  await page.getByRole('tab', { name: '发送记录', exact: true }).click()
  await expect.poll(() => records).toBe(1)
})

test('backup integrity checks consume queued task results and expose failures', async ({
  page,
}) => {
  await page.route('**/api/v1/settings/backups', (r) =>
    r.fulfill({
      json: [
        {
          name: 'fixture.ovp',
          size: 123,
          createdAt: '2026-11-19T01:00:00Z',
          storage: '/www/backup',
          url: '/fixture.ovp',
        },
      ],
    }),
  )
  await page.route('**/api/v1/backups/archives', (r) => r.fulfill({ json: { rows: [] } }))
  let attempt = 0,
    reads = 0
  await page.route('**/api/v1/backups/archives/verify', (r) => {
    attempt++
    reads = 0
    return r.fulfill({ status: 202, json: { id: 'archive-verify' } })
  })
  await page.route('**/api/v1/backups/tasks/archive-verify', (r) => {
    reads++
    return r.fulfill({
      json:
        reads === 1
          ? { state: 'running' }
          : attempt === 1
            ? { state: 'failed', error: '原密钥缺失' }
            : { state: 'succeeded', result: { verified: true } },
    })
  })
  await page.goto('/backups?tab=records')
  const row = page.getByRole('row').filter({ hasText: 'fixture.ovp' })
  await row.getByRole('button', { name: '校验', exact: true }).click()
  await expect(row).toContainText('处理中')
  await expect(page.getByText('原密钥缺失', { exact: true })).toBeVisible()
  await expect(row).toContainText('待校验')
  await row.getByRole('button', { name: '校验', exact: true }).click()
  await expect(row).toContainText('已校验')
})

// Theme checks use real computed styles, including overlays teleported to body.
async function themeStyle(locator: Locator) {
  await locator.evaluate(async (el) => {
    await Promise.all(el.getAnimations().map((animation) => animation.finished.catch(() => {})))
  })
  return locator.evaluate((el) => {
    const style = getComputedStyle(el)
    const hex = (value: string) => {
      const channels = value.match(/[\d.]+/g)!.slice(0, 3)
      return '#' + channels.map((v) => Number(v).toString(16).padStart(2, '0')).join('')
    }
    return { color: hex(style.color), background: hex(style.backgroundColor) }
  })
}
async function mockTheme(page: Page, theme: string, primaryColor = '#2563eb') {
  for (const endpoint of ['public/preferences', 'settings', 'settings/groups/appearance']) {
    await page.route(`**/api/v1/${endpoint}`, async (route) => {
      const response = await route.fetch()
      const data = await response.json()
      const config = { theme, primaryColor }
      await route.fulfill({
        json: endpoint.includes('groups')
          ? { ...data, config: { ...data.config, ...config } }
          : { ...data, ...config },
      })
    })
  }
}
for (const mode of ['light', 'dark']) {
  test(`${mode} theme shares colors across navigation, lists and fixed overlays`, async ({
    page,
  }) => {
    await mockTheme(page, mode)
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto('/')
    await expect(page.locator('.metric-card').first()).toBeVisible()
    const surface = await themeStyle(page.locator('.metric-card').first())
    expect(surface.background).toBe(mode === 'dark' ? '#182230' : '#ffffff')
    expect(contrast(surface.color, surface.background)).toBeGreaterThanOrEqual(4.5)
    const active = await themeStyle(page.locator('.sidebar nav a.active'))
    expect(contrast(active.color, active.background)).toBeGreaterThanOrEqual(4.5)
    await page.screenshot({
      path: `../.opsview/screenshots/theme-${mode}-overview.png`,
      fullPage: true,
    })
    for (const url of [
      '/software',
      '/sites',
      '/databases',
      '/docker',
      '/files',
      '/backups',
      '/system',
      '/settings',
      '/tasks',
    ]) {
      await page.locator(`.sidebar a[href="${url}"]`).click()
      await expect(page).toHaveURL(url)
      await expect(page.locator('.navigation-progress')).toHaveCount(0)
      await expect(page.locator('.surface').first()).toBeVisible()
      expect((await themeStyle(page.locator('.surface').first())).background).toBe(
        surface.background,
      )
      const header = page.locator('.el-table__header th').first()
      if (await header.count()) {
        const style = await themeStyle(header)
        expect(style.background).toBe(mode === 'dark' ? '#1d2938' : '#f6f8fb')
        expect(contrast(style.color, style.background)).toBeGreaterThanOrEqual(4.5)
      }
    }
    await page.goto('/settings')
    await page.getByRole('tab', { name: '告警通知', exact: true }).click()
    await page.getByRole('button', { name: '添加告警规则', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '添加告警规则', exact: true })
    await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeVisible()
    const style = await themeStyle(dialog.locator('.el-dialog'))
    expect(style.background).toBe(mode === 'dark' ? '#202d3d' : '#ffffff')
    const button = await themeStyle(dialog.getByRole('button', { name: '保存', exact: true }))
    expect(button.background).toBe(active.color)
    expect(contrast(button.color, button.background)).toBeGreaterThanOrEqual(4.5)
    await dialog.getByRole('button', { name: '保存', exact: true }).hover()
    const hovered = await themeStyle(dialog.getByRole('button', { name: '保存', exact: true }))
    expect(contrast(hovered.color, hovered.background)).toBeGreaterThanOrEqual(4.5)
    await dialog.getByRole('combobox', { name: '类型', exact: true }).press('ArrowDown')
    const option = page.getByRole('option', { name: 'CPU 使用率', exact: true })
    await expect(option).toBeVisible()
    const popup = page.locator('.el-select__popper:visible')
    expect((await themeStyle(popup)).background).toBe(style.background)
    await page.keyboard.press('Escape')
    await page.screenshot({
      path: `../.opsview/screenshots/theme-${mode}-settings.png`,
      fullPage: true,
    })
    await page.setViewportSize({ width: 390, height: 600 })
    const geometry = await overlayGeometry(dialog)
    await dialog.locator('.el-dialog__body').evaluate((el) => (el.scrollTop = el.scrollHeight))
    expectFixedOverlay(geometry, await overlayGeometry(dialog))
    await expect(dialog.getByRole('button', { name: '保存', exact: true })).toBeInViewport()
    await page.screenshot({
      path: `../.opsview/screenshots/theme-${mode}-mobile.png`,
      fullPage: true,
    })
  })
}

test('overview theme dropdown persists modes and preserves the saved mode on failure', async ({
  page,
}) => {
  let theme = 'light',
    revision = 4
  const writes: any[] = []
  await page.route('**/api/v1/settings/groups/appearance', async (r) => {
    if (r.request().method() === 'PATCH') {
      const body = r.request().postDataJSON()
      expect(body.revision).toBe(revision)
      expect(Object.keys(body.patch)).toEqual(['theme'])
      writes.push(body.patch.theme)
      if (body.patch.theme === 'dark')
        return r.fulfill({ status: 409, json: { error: '主题已被其他会话修改' } })
      theme = body.patch.theme
      revision++
    }
    return r.fulfill({ json: { revision, config: { theme, primaryColor: '#2563eb' } } })
  })
  await page.goto('/')
  await expect(page.locator('.metric-card').first()).toBeVisible()
  await page.getByRole('button', { name: '切换主题', exact: true }).click()
  await expect(page.getByRole('menuitem')).toHaveText(['跟随系统', '亮色 ✓', '黑色'])
  await page.getByRole('menuitem', { name: '黑色', exact: true }).click()
  await expect(page.getByText('主题已被其他会话修改', { exact: true })).toBeVisible()
  await expect(page.locator('html')).not.toHaveClass('dark')
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.getByRole('button', { name: '切换主题', exact: true }).click()
  await page.getByRole('menuitem', { name: '跟随系统', exact: true }).click()
  await expect(page.locator('html')).toHaveClass('dark')
  await page.emulateMedia({ colorScheme: 'light' })
  await expect(page.locator('html')).not.toHaveClass('dark')
  expect(writes).toEqual(['dark', 'auto'])
  await page.goto('/settings')
  await expect(page.getByRole('tab', { name: '界面设置', exact: true })).toHaveCount(0)
  await expect(page.getByText('面板名称', { exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: '面板设置', exact: true }).click()
  await expect(page.getByText('侧栏标题', { exact: true })).toHaveCount(0)
})

test('default appearance stays light on a dark system until follow-system is selected', async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.goto('/')
  await expect(page.locator('.metric-card').first()).toBeVisible()
  await expect(page.locator('html')).not.toHaveClass('dark')
  await mockTheme(page, 'auto')
  await page.reload()
  await expect(page.locator('html')).toHaveClass('dark')
  await page.emulateMedia({ colorScheme: 'light' })
  await expect(page.locator('html')).not.toHaveClass('dark')
})

test('dark software editor and error state have no light surface remnants', async ({ page }) => {
  await mockTheme(page, 'dark', '#ffffff')
  await page.route('**/api/v1/resources', (r) => r.fulfill({ json: [nginxFixture] }))
  await page.route('**/api/v1/instances/nginx-fixture/settings', (r) =>
    r.fulfill({
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
  await page.route('**/api/v1/instances/nginx-fixture/configuration?*', (r) =>
    r.fulfill({
      json: {
        revision: 'a'.repeat(64),
        content: 'worker_processes auto;\nevents { worker_connections 1024; }\nhttp {}',
        values: {},
        fields: [],
        history: [],
      },
    }),
  )
  await page.route('**/api/v1/instances/nginx-fixture/validate', (r) =>
    r.fulfill({ status: 409, json: { error: '配置已被修改，请重新载入' } }),
  )
  await page.goto('/software')
  await page
    .locator('.software-list .el-table__body tr')
    .filter({ hasText: 'Nginx' })
    .getByRole('button', { name: '设置', exact: true })
    .click()
  const dialog = page.locator('.software-settings')
  await dialog.locator('aside').getByRole('button', { name: '配置文件', exact: true }).click()
  const editor = dialog.locator('.cm-editor')
  await expect(editor).toBeVisible()
  const editorStyle = await themeStyle(editor)
  expect(editorStyle.background).toBe('#182230')
  expect(contrast(editorStyle.color, editorStyle.background)).toBeGreaterThanOrEqual(4.5)
  expect((await themeStyle(dialog.locator('.cm-gutters'))).background).toBe('#1d2938')
  const geometry = await overlayGeometry(dialog)
  await dialog.getByRole('button', { name: '校验配置', exact: true }).click()
  const alert = dialog.locator('.el-alert')
  await expect(alert).toContainText('配置已被修改')
  const errorStyle = await themeStyle(alert)
  expect(contrast(errorStyle.background, '#ffffff')).toBeGreaterThan(8)
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  const color = await themeStyle(dialog.getByRole('button', { name: '保存并重载', exact: true }))
  expect(contrast(color.color, color.background)).toBeGreaterThanOrEqual(4.5)
  await page.screenshot({ path: '../.opsview/screenshots/theme-dark-editor.png', fullPage: true })
})

test('settings edit values inline, preserve siblings, and clear secret forms when switching tabs', async ({
  page,
}) => {
  let refreshSeconds = 15,
    revision = 9
  await page.route('**/api/v1/settings/groups/panel', async (r) => {
    if (r.request().method() === 'PATCH') {
      const body = r.request().postDataJSON()
      expect(body.patch).toEqual({ refreshSeconds: 30 })
      expect(body.revision).toBe(revision)
      refreshSeconds = body.patch.refreshSeconds
      revision++
    }
    return r.fulfill({ json: { config: { refreshSeconds, timezone: 'Local' }, revision } })
  })
  await page.goto('/settings')
  const row = page
    .locator('.preference-item')
    .filter({ has: page.getByText('刷新频率', { exact: true }) })
  await row.getByRole('button', { name: '修改刷新频率', exact: true }).click()
  await expect(row.getByRole('combobox')).toBeEnabled()
  await row.getByRole('combobox').press('ArrowDown')
  await page.getByRole('option', { name: '30 秒', exact: true }).click()
  await row.getByRole('button', { name: '保存', exact: true }).click()
  await expect(row.getByRole('button', { name: '修改刷新频率', exact: true })).toHaveText('30 秒')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('tab', { name: '安全设置', exact: true }).click()
  const passwordRow = page
    .locator('.preference-item')
    .filter({ has: page.getByText('管理员密码', { exact: true }) })
  await passwordRow.getByRole('button', { name: '修改管理员密码', exact: true }).click()
  await passwordRow.getByRole('textbox', { name: '当前密码', exact: true }).fill('draft-secret')
  await page.getByRole('tab', { name: '面板设置', exact: true }).click()
  await page.getByRole('tab', { name: '安全设置', exact: true }).click()
  await passwordRow.getByRole('button', { name: '修改管理员密码', exact: true }).click()
  await expect(passwordRow.getByRole('textbox', { name: '当前密码', exact: true })).toHaveValue('')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('tab', { name: '告警通知', exact: true }).click()
  const tabs = page.locator('.notification-tabs')
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await expect(tabs).toBeVisible()
    const bounds = await tabs.boundingBox()
    const panel = await page.locator('.notification-manager').boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(panel!.x)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(panel!.x + panel!.width + 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  }
  await page.screenshot({
    path: '../.opsview/screenshots/rc19-notifications-mobile.png',
    fullPage: true,
    animations: 'disabled',
  })
})

test('login requests a verifier code only after a correct password and supports returning', async ({
  page,
}) => {
  await page.route('**/api/v1/auth', (r) =>
    r.fulfill({ json: { authenticated: false, setupRequired: false } }),
  )
  let submissions = 0
  await page.route('**/api/v1/auth/login', (r) => {
    submissions++
    const body = r.request().postDataJSON()
    return r.fulfill({
      status: body.code ? 401 : 428,
      json: body.code
        ? { error: '账号、密码或验证码错误' }
        : { code: 'totp_required', error: '请完成验证器验证' },
    })
  })
  await page.goto('/')
  await expect(page.getByRole('textbox', { name: '验证码', exact: true })).toHaveCount(0)
  await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
  await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '进入控制台', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '验证码', exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '密码', exact: true })).toHaveCount(0)
  await page.getByRole('textbox', { name: '验证码', exact: true }).fill('123456')
  await page.getByRole('button', { name: '验证并登录', exact: true }).click()
  await expect(page.getByText('账号、密码或验证码错误', { exact: true })).toBeVisible()
  expect(submissions).toBe(2)
  await page.getByRole('button', { name: '返回账号登录', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '验证码', exact: true })).toHaveCount(0)
  await expect(page.getByRole('textbox', { name: '密码', exact: true })).toHaveValue('')
})

test('security entrance edits inline and stages a reconnect confirmation without changing other access values', async ({
  page,
}) => {
  const config = {
    protocol: 'https',
    listenHost: '0.0.0.0',
    port: 18790,
    publicHost: 'panel.example.com',
    entry: 'original_entrance',
    allowedHost: '',
    allowedCIDRs: '',
  }
  await page.route('**/api/v1/settings/runtime', async (r) => {
    if (r.request().method() === 'PUT') {
      const body = r.request().postDataJSON()
      expect(body.config).toEqual({ ...config, entry: 'new_entrance_2026' })
      expect(body.password).toBe(password)
      return r.fulfill({
        json: {
          url: 'https://panel.example.com:18790/new_entrance_2026',
          confirmToken: 'b'.repeat(64),
        },
      })
    }
    return r.fulfill({ json: { config } })
  })
  await page.goto('/settings')
  await page.getByRole('tab', { name: '面板设置', exact: true }).click()
  const row = page
    .locator('.preference-item')
    .filter({ has: page.getByText('安全入口', { exact: true }) })
  await row.getByRole('button', { name: '修改安全入口', exact: true }).click()
  await row.getByRole('textbox', { name: '安全入口', exact: true }).fill('new_entrance_2026')
  await row.getByRole('button', { name: '保存', exact: true }).click()
  await page.getByRole('button', { name: '确认修改', exact: true }).click()
  await page.getByRole('textbox', { name: '请输入当前管理员密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '验证', exact: true }).click()
  await expect(
    page.getByRole('link', { name: '打开新地址并确认（120 秒内）', exact: true }),
  ).toHaveAttribute('href', /^https:.*new_entrance_2026#runtime-confirm=/)
  await expect(row.getByRole('textbox')).toHaveCount(0)
})

test('closing an inline read does not leave subsequent settings busy and security managers expand in place', async ({
  page,
}) => {
  await page.route('**/api/v1/settings/runtime', (r) =>
    r.fulfill({
      json: {
        config: {
          protocol: 'https',
          port: 18790,
          listenHost: '0.0.0.0',
          publicHost: 'panel.example.com',
        },
      },
    }),
  )
  let release!: () => void
  const barrier = new Promise<void>((r) => {
    release = r
  })
  await page.route('**/api/v1/settings/groups/panel', async (r) => {
    await barrier
    await r.fulfill({
      json: { config: { siteDirectory: '/www/wwwroot', refreshSeconds: 15 }, revision: 3 },
    })
  })
  await page.goto('/settings')
  const row = page
    .locator('.preference-item')
    .filter({ has: page.getByText('默认建站目录', { exact: true }) })
  await row.getByRole('button', { name: '修改默认建站目录', exact: true }).click()
  await expect(row.getByRole('textbox')).toBeDisabled()
  await page.getByRole('tab', { name: '安全设置', exact: true }).click()
  release()
  await page.route('**/api/v1/auth/sessions', (r) => r.fulfill({ json: [] }))
  const sessions = page
    .locator('.preference-item')
    .filter({ has: page.getByText('登录会话', { exact: true }) })
  await sessions.getByRole('button', { name: '修改登录会话', exact: true }).click()
  await expect(sessions.getByRole('button', { name: '全部退出', exact: true })).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await sessions.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('tab', { name: '常用设置', exact: true }).click()
  await row.getByRole('button', { name: '修改默认建站目录', exact: true }).click()
  await expect(row.getByRole('textbox')).toBeEnabled()
  await row.getByRole('textbox').fill('/draft')
  await row.getByRole('textbox').press('Escape')
  await expect(row.getByRole('textbox')).toHaveCount(0)
  await expect(row.getByRole('button', { name: '修改默认建站目录', exact: true })).toHaveText(
    '/www/wwwroot',
  )
})

test('changing theme keeps the configured polling interval', async ({ page }) => {
  await page.clock.install()
  let resourceReads = 0,
    preferencesRead = false
  await page.route('**/api/v1/resources', (r) => {
    resourceReads++
    return r.fulfill({ json: [] })
  })
  await page.route('**/api/v1/settings', async (r) => {
    const response = await r.fetch()
    const data = await response.json()
    preferencesRead = true
    return r.fulfill({ json: { ...data, theme: 'light', refreshSeconds: 5 } })
  })
  await page.route('**/api/v1/public/preferences', (r) => r.fulfill({ json: { theme: 'light' } }))
  await page.route('**/api/v1/settings/groups/appearance', (r) =>
    r.fulfill({
      json: { revision: 2, config: { theme: r.request().method() === 'PATCH' ? 'dark' : 'light' } },
    }),
  )
  await page.goto('/')
  await expect.poll(() => preferencesRead).toBe(true)
  await page.getByRole('button', { name: '切换主题', exact: true }).click()
  await page.getByRole('menuitem', { name: '黑色', exact: true }).click()
  await expect(page.locator('html')).toHaveClass('dark')
  const previous = resourceReads
  await page.clock.runFor(5200)
  await expect.poll(() => resourceReads, { timeout: 1500 }).toBeGreaterThan(previous)
})

test('static site creation needs no PHP, binds a database and hides advanced controls', async ({
  page,
}) => {
  let submitted: any
  const mysql = {
    ...nginxFixture,
    id: 'mysql-choice',
    name: 'mysql-8.4.13',
    config: { software: 'mysql', version: '8.4.13', mode: 'native', port: 3306 },
  }
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginxFixture, mysql] }))
  await page.route('**/api/v1/site-categories', (route) =>
    route.fulfill({ json: { rows: ['默认分类', '业务'] } }),
  )
  await page.route('**/api/v1/sites', (route) => {
    submitted = route.request().postDataJSON()
    return route.fulfill({
      status: 202,
      json: { siteId: 'static-new', databaseId: 'db-new', taskId: 'create-task' },
    })
  })
  await page.route('**/api/v1/tasks/create-task', (route) =>
    route.fulfill({ json: { id: 'create-task', state: 'succeeded' } }),
  )
  await page.goto('/sites')
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('static.example.com')
  await expect(dialog.getByRole('button', { name: '高级设置', exact: true })).toHaveCount(0)
  await expect(dialog).not.toContainText('Nginx 入口')
  await expect(dialog.locator('.el-form-item').filter({ hasText: 'PHP 版本' })).toContainText(
    '静态',
  )
  await dialog
    .locator('.el-form-item')
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^数据库$/ }) })
    .locator('.el-select')
    .click()
  await page.getByRole('option', { name: /mysql-8.4.13/ }).click()
  const fields = dialog.locator('.el-form-item')
  await fields
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^数据库名$/ }) })
    .locator('input')
    .fill('site_static')
  await fields
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^专用账号$/ }) })
    .locator('input')
    .fill('static_user')
  await fields
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^数据库密码$/ }) })
    .locator('input')
    .fill('new-only-db-password')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(submitted.config.type).toBe('static')
  expect(submitted.config.phpInstanceId).toBeUndefined()
  expect(submitted.config.rootDirectory).toBe('/www/wwwroot/static.example.com')
  expect(submitted.database).toMatchObject({
    instanceId: 'mysql-choice',
    name: 'site_static',
    username: 'static_user',
  })
  expect(submitted.config.password).toBeUndefined()
})

test('file context menu preserves batch selection and permission edits change only explicit bits', async ({
  page,
}) => {
  const items = [
    {
      name: 'first.txt',
      path: '/first.txt',
      mode: '0644',
      owner: 'root',
      group: 'staff',
      size: 1,
      directory: false,
      revision: 'one',
    },
    {
      name: 'second.txt',
      path: '/second.txt',
      mode: '0600',
      owner: 'www-data',
      group: 'www-data',
      size: 2,
      directory: false,
      revision: 'two',
    },
  ]
  let saved: any
  await page.route('**/api/v1/files', (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'metadata') return route.fulfill({ json: { favorites: [], shortcuts: [] } })
    if (p.action === 'list')
      return route.fulfill({ json: { path: '/', items, total: 2, readOnly: false } })
    if (p.action === 'identities')
      return route.fulfill({
        json: {
          users: [
            { name: 'root', id: 0 },
            { name: 'www-data', id: 33 },
          ],
          groups: [
            { name: 'staff', id: 50 },
            { name: 'www-data', id: 33 },
          ],
        },
      })
    if (p.action === 'permissions') saved = p
    return route.fulfill({ json: {} })
  })
  await page.goto('/files')
  const rows = page.locator('.data-list .el-table__body tr')
  await expect(rows).toHaveCount(2)
  await rows.nth(0).locator('.el-checkbox').click()
  await rows.nth(1).locator('.el-checkbox').click()
  await page.getByRole('button', { name: 'first.txt', exact: true }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: '权限', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '权限与所有者', exact: true })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByPlaceholder('保持混合权限')).toHaveValue('')
  await dialog
    .locator('.file-permission-matrix section')
    .first()
    .getByText('执行', { exact: true })
    .click()
  await expect(dialog.getByPlaceholder('保持混合权限')).toHaveValue('')
  await dialog.getByRole('button', { name: '应用', exact: true }).click()
  await page
    .getByRole('dialog', { name: '确认权限变更', exact: true })
    .getByRole('button', { name: '应用', exact: true })
    .click()
  await expect.poll(() => saved?.permissionMask).toBe(64)
  expect(saved.permissionValue).toBe(64)
  expect(saved.paths).toEqual(['/first.txt', '/second.txt'])
  expect(saved.owner).toBe('')
  expect(saved.group).toBe('')
  expect(saved.revisions).toEqual({ '/first.txt': 'one', '/second.txt': 'two' })
  await expect(dialog).toBeHidden()
  await page.getByRole('button', { name: 'second.txt', exact: true }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: '剪切', exact: true }).click()
  await expect(rows.nth(1)).toHaveClass(/file-row-cut/)
  await page.getByRole('button', { name: 'first.txt', exact: true }).click({ button: 'right' })
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
})

test('website creation waits for a real outcome and resumes without duplicate creation', async ({
  page,
}) => {
  let creates = 0,
    resumes = 0,
    state = 'running'
  const site = {
    id: 'draft-site',
    kind: 'site',
    name: 'draft.test',
    revision: 2,
    status: 'error',
    config: {
      type: 'static',
      domains: 'draft.test',
      instanceId: nginxFixture.id,
      rootDirectory: '/www/wwwroot/draft.test',
    },
  }
  await page.route('**/api/v1/resources', (route) =>
    route.fulfill({ json: creates ? [nginxFixture, site] : [nginxFixture] }),
  )
  await page.route('**/api/v1/sites/provisions/*', (route) =>
    creates
      ? route.fulfill({
          json: { siteId: site.id, taskId: 'creation-observer', state, revision: 2 },
        })
      : route.fulfill({ status: 404, json: { error: 'not registered' } }),
  )
  await page.route('**/api/v1/sites', (route) => {
    creates++
    return route.fulfill({ status: 202, json: { siteId: site.id, taskId: 'creation-observer' } })
  })
  await page.route('**/api/v1/sites/draft-site/provision', (route) => {
    resumes++
    state = 'succeeded'
    return route.fulfill({ status: 202, json: { siteId: site.id, taskId: 'creation-observer' } })
  })
  await page.route('**/api/v1/tasks/creation-observer', (route) =>
    route.fulfill({
      json: {
        id: 'creation-observer',
        state,
        error: state === 'failed' ? 'Nginx 配置校验失败，原服务保留' : '',
      },
    }),
  )
  await page.goto('/sites')
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('draft.test')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog.getByRole('button', { name: '创建中', exact: true })).toBeDisabled()
  await expect(dialog).toBeVisible()
  state = 'failed'
  await expect(dialog).toContainText('Nginx 配置校验失败')
  await expect(dialog.getByRole('button', { name: /重试|等待/ })).toHaveCount(0)
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(creates).toBe(1)
  expect(resumes).toBe(1)
})

test('website selection, refresh and safe batch deletion use one fixed dialog', async ({
  page,
}) => {
  let reads = 0,
    previewCount = 0,
    options: any[] = [],
    submitted = false
  const sites = ['first.test', 'second.test'].map((name, index) => ({
    id: `batch-${index}`,
    name,
    kind: 'site',
    status: 'ready',
    revision: 1,
    config: {
      type: 'static',
      domains: name,
      documentRoot: `/www/wwwroot/${name}`,
      instanceId: nginxFixture.id,
    },
  }))
  await page.route('**/api/v1/resources', (route) => {
    reads++
    return route.fulfill({ json: [nginxFixture, ...sites] })
  })
  await page.route('**/api/v1/sites/delete-preview', (route) => {
    previewCount++
    options = route.request().postDataJSON().items
    return route.fulfill({
      json: {
        previewId: 'batch-preview',
        confirmAfterSeconds: 5,
        items: options.map((o) => ({
          ...o,
          name: sites.find((s) => s.id === o.id)?.name,
          directory: '/www/wwwroot/example',
          databaseReason: '未关联受管数据库',
          services: [],
          schedules: [],
          inspection: { phase: '未执行' },
        })),
      },
    })
  })
  await page.route('**/api/v1/sites/delete', (route) => {
    submitted = true
    return route.fulfill({ status: 202, json: { taskId: 'batch-delete' } })
  })
  await page.route('**/api/v1/tasks/batch-delete', (route) =>
    route.fulfill({ json: { id: 'batch-delete', state: 'failed' } }),
  )
  await page.route('**/api/v1/sites/delete-tasks/batch-delete', (route) =>
    route.fulfill({
      json: {
        state: 'failed',
        error: '1 个站点删除未完成',
        result: {
          items: [
            { id: 'batch-0', name: 'first.test', state: 'succeeded' },
            {
              id: 'batch-1',
              name: 'second.test',
              state: 'failed',
              error: '回收站不可用，数据保留',
            },
          ],
        },
      },
    }),
  )
  await page.goto('/sites')
  await expect(page.getByRole('button', { name: '批量删除', exact: true })).toBeDisabled()
  await page.locator('.site-table .el-table__body tr').first().locator('.el-checkbox').click()
  await page.getByPlaceholder('搜索名称、域名或备注').fill('second')
  await expect(page.getByRole('button', { name: '批量删除', exact: true })).toBeDisabled()
  const priorReads = reads
  await page.getByRole('button', { name: '刷新网站列表', exact: true }).click()
  await expect.poll(() => reads).toBeGreaterThan(priorReads)
  await expect(page.getByPlaceholder('搜索名称、域名或备注')).toHaveValue('second')
  await page.getByPlaceholder('搜索名称、域名或备注').clear()
  await page.locator('.site-table .el-table__header .el-checkbox').click()
  await page.getByRole('button', { name: /批量删除\s*（2）/ }).click()
  const dialog = page.getByRole('dialog', { name: '删除网站', exact: true })
  await expect(
    dialog.getByRole('checkbox', { name: '项目目录', exact: true }).first(),
  ).not.toBeChecked()
  expect(options.every((o) => !o.deleteDatabase && !o.deleteDirectory && !o.deleteLogs)).toBe(true)
  await dialog.locator('.el-checkbox').filter({ hasText: '已了解删除影响' }).click()
  await expect(dialog.getByRole('button', { name: /确认删除/ })).toBeDisabled()
  await dialog.locator('.el-checkbox').filter({ hasText: '项目目录' }).first().click()
  await expect.poll(() => previewCount).toBeGreaterThan(1)
  await expect(dialog.getByRole('checkbox', { name: '已了解删除影响' })).not.toBeChecked()
  await dialog.locator('.el-checkbox').filter({ hasText: '已了解删除影响' }).click()
  await expect(dialog.getByRole('button', { name: '确认删除', exact: true })).toBeEnabled({
    timeout: 10000,
  })
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click()
  await expect(dialog).toContainText('回收站不可用，数据保留')
  expect(submitted).toBe(true)
  await page.setViewportSize({ width: 390, height: 844 })
  const geometry = await overlayGeometry(dialog)
  await dialog.locator('.el-dialog__body').evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeInViewport()
})

for (const mode of ['light', 'dark']) {
  test(`${mode} site deletion countdown stays legible and disabled until confirmed`, async ({
    page,
  }) => {
    await mockTheme(page, mode)
    const site = {
      id: 'countdown-site',
      kind: 'site',
      name: 'countdown.test',
      status: 'ready',
      revision: 1,
      config: { type: 'static', domains: 'countdown.test', instanceId: nginxFixture.id },
    }
    await page.route('**/api/v1/resources', (route) =>
      route.fulfill({ json: [nginxFixture, site] }),
    )
    await page.route('**/api/v1/sites/delete-preview', (route) =>
      route.fulfill({
        json: {
          previewId: 'countdown-preview',
          confirmAfterSeconds: 5,
          items: [
            {
              id: site.id,
              name: site.name,
              directoryReason: '目录保留',
              databaseReason: '未关联受管数据库',
              inspection: { phase: '站点配置已不存在；已核验归属的独立 PHP-FPM 残留服务将清理' },
            },
          ],
        },
      }),
    )
    await page.goto('/sites')
    await page
      .locator('.site-table .el-table__body tr')
      .first()
      .getByRole('button', { name: '删除', exact: true })
      .click()
    const dialog = page.getByRole('dialog', { name: '删除网站', exact: true })
    const button = dialog.getByRole('button', { name: /确认删除/ })
    await expect(button).toContainText('秒')
    await expect(button).toBeDisabled()
    await overlayGeometry(dialog)
    const style = await themeStyle(button)
    expect(contrast(style.color, style.background)).toBeGreaterThanOrEqual(4.5)
    expect(await button.evaluate((el) => getComputedStyle(el).opacity)).toBe('1')
    await page.screenshot({
      path: `../.opsview/screenshots/site-delete-countdown-${mode}.png`,
      fullPage: true,
    })
    await expect(button).toHaveText('确认删除', { timeout: 10000 })
    await expect(button).toBeDisabled()
    await dialog.locator('.el-checkbox').filter({ hasText: '已了解删除影响' }).click()
    await expect(button).toBeEnabled()
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(button).toBeInViewport()
    await expect(dialog.getByRole('button', { name: '取消', exact: true })).toBeInViewport()
    await page.screenshot({
      path: `../.opsview/screenshots/site-delete-countdown-${mode}-mobile.png`,
      fullPage: true,
    })
  })
}

test('website domain bindings apply multiline additions, retain failed drafts and keep refresh beside search', async ({
  page,
}) => {
  const site: any = {
    id: 'domain-settings-site',
    kind: 'site',
    name: 'domain.test',
    status: 'ready',
    revision: 1,
    config: {
      type: 'static',
      domains: 'domain.test',
      instanceId: nginxFixture.id,
      documentRoot: '/www/wwwroot/domain.test',
    },
  }
  let fail = true,
    posts = 0
  await page.route('**/api/v1/resources', (r) => r.fulfill({ json: [nginxFixture, site] }))
  await page.route('**/api/v1/sites/domain-settings-site/traffic', (r) =>
    r.fulfill({ json: { todayBytes: 0, hours: {} } }),
  )
  await page.route('**/api/v1/sites/domain-settings-site/settings', (r) => {
    posts++
    const value = r.request().postDataJSON()
    expect(value.revision).toBe(site.revision)
    if (fail) return r.fulfill({ status: 422, json: { error: 'Nginx 配置未通过，原站点继续运行' } })
    site.config = value.config
    site.name = value.name
    site.revision++
    return r.fulfill({ status: 202, json: { taskId: 'domain-apply' } })
  })
  await page.route('**/api/v1/tasks/domain-apply', (r) =>
    r.fulfill({ json: { id: 'domain-apply', state: 'succeeded' } }),
  )
  await page.goto('/sites')
  await expect(page.locator('.site-toolbar')).not.toContainText('个项目')
  const adjacent = await page
    .locator('.site-toolbar .el-input')
    .evaluate((el) => el.nextElementSibling?.getAttribute('aria-label'))
  expect(adjacent).toBe('刷新网站列表')
  await page.getByRole('button', { name: 'domain.test', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'domain.test · 网站设置', exact: true })
  const input = dialog.getByRole('textbox', { name: '添加绑定域名', exact: true })
  await input.fill('www.domain.test\napi.domain.test:8081')
  await dialog.getByRole('button', { name: '添加域名', exact: true }).click()
  await expect(dialog).toContainText('Nginx 配置未通过')
  await expect(input).toHaveValue('www.domain.test\napi.domain.test:8081')
  await expect(dialog.getByRole('link', { name: 'www.domain.test', exact: true })).toHaveCount(0)
  fail = false
  await dialog.getByRole('button', { name: '添加域名', exact: true }).click()
  await expect(dialog.getByRole('link', { name: 'www.domain.test', exact: true })).toBeVisible()
  expect(site.config.domains.split(/\s+/)).toEqual([
    'domain.test',
    'www.domain.test',
    'api.domain.test:8081',
  ])
  const link = dialog.getByRole('link', { name: 'api.domain.test', exact: true })
  await expect(link).toHaveAttribute('href', 'http://api.domain.test:8081/')
  await expect(link).toHaveAttribute('target', '_blank')
  await expect(input).toHaveValue('')
  fail = true
  await input.fill('pending.domain.test')
  await dialog.getByRole('button', { name: '添加域名', exact: true }).click()
  await expect(dialog).toContainText('Nginx 配置未通过')
  fail = false
  await dialog
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name: 'www.domain.test', exact: true }) })
    .getByRole('button', { name: '解除', exact: true })
    .click()
  await page
    .getByRole('dialog', { name: '解除域名绑定', exact: true })
    .getByRole('button', { name: '解除绑定', exact: true })
    .click()
  await expect(dialog.getByRole('link', { name: 'www.domain.test', exact: true })).toHaveCount(0)
  await expect(dialog).toBeVisible()
  expect(site.config.domains.split(/\s+/)).toEqual(['domain.test', 'api.domain.test:8081'])
  await expect(input).toHaveValue('pending.domain.test')
  expect(posts).toBe(4)
})

for (const mode of ['light', 'dark']) {
  test(`${mode} website setting menus preserve fixed dialog layout and expose real policy forms`, async ({
    page,
  }) => {
    await mockTheme(page, mode)
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    const site: any = {
      id: 'all-settings-site',
      kind: 'site',
      name: 'settings.test',
      status: 'ready',
      revision: 2,
      config: {
        type: 'static',
        domains: 'settings.test',
        instanceId: nginxFixture.id,
        documentRoot: '/www/wwwroot/settings.test',
        advanced: '',
      },
    }
    await page.route('**/api/v1/resources', (r) => r.fulfill({ json: [nginxFixture, site] }))
    await page.route('**/api/v1/sites/all-settings-site/*', (r) => {
      const path = new URL(r.request().url()).pathname.split('/').at(-1)
      return r.fulfill({
        json:
          path === 'configuration'
            ? {
                current: 'server { listen 80; }',
                history: [{ revision: 'before-apply', content: 'server { listen 81; }' }],
                drift: false,
              }
            : path === 'security'
              ? { checks: [{ name: '目录权限', status: 'pass', detail: '未开放全局写入' }] }
              : path === 'directories'
                ? { directories: ['public', 'docs'] }
                : path === 'logs'
                  ? { content: 'actual-log-line' }
                  : { todayBytes: 0, hours: {} },
      })
    })
    await page.route('**/api/v1/notifications/rules', (r) => r.fulfill({ json: [] }))
    await page.route('**/api/v1/notifications/channels', (r) => r.fulfill({ json: [] }))
    await page.goto('/sites')
    await page.getByRole('button', { name: 'settings.test', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'settings.test · 网站设置', exact: true })
    const geometry = await overlayGeometry(dialog)
    for (const label of [
      '子目录绑定',
      '网站目录',
      '访问限制',
      '流量限制',
      '伪静态',
      '默认文档',
      '配置文件',
      'SSL 证书',
      'PHP',
      '重定向',
      '反向代理',
      '防盗链',
      '防篡改',
      '网站安全',
      '网站日志',
      '网站告警',
      '其他设置',
      '流量统计',
      '到期时间',
      '分类与备注',
      '网站备份',
      '域名管理',
    ]) {
      await dialog.locator('aside').getByRole('button', { name: label, exact: true }).click()
      await expectFixedOverlay(geometry, await overlayGeometry(dialog))
    }
    await dialog.locator('aside').getByRole('button', { name: '其他设置', exact: true }).click()
    await dialog.getByRole('tab', { name: '响应头', exact: true }).click()
    await expect(
      dialog.getByRole('textbox', { name: 'Content-Security-Policy', exact: true }),
    ).toBeVisible()
    await page.screenshot({
      path: `../.opsview/screenshots/site-settings-${mode}.png`,
      animations: 'disabled',
    })
    await page.setViewportSize({ width: 390, height: 844 })
    const before = await overlayGeometry(dialog)
    await dialog.locator('.ops-settings-content').evaluate((el) => (el.scrollTop = el.scrollHeight))
    expectFixedOverlay(before, await overlayGeometry(dialog))
    await expect(dialog.getByRole('button', { name: '保存并应用', exact: true })).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(392)
    await page.screenshot({
      path: `../.opsview/screenshots/site-settings-${mode}-mobile.png`,
      animations: 'disabled',
    })
    expect(errors).toEqual([])
  })
}

test('website policy changes keep the revision and stay in the current menu after real task completion', async ({
  page,
}) => {
  const site: any = {
    id: 'policy-settings-site',
    kind: 'site',
    name: 'policies.test',
    status: 'ready',
    revision: 7,
    config: { type: 'static', domains: 'policies.test', instanceId: nginxFixture.id },
  }
  let submitted: any
  await page.route('**/api/v1/resources', (r) => r.fulfill({ json: [nginxFixture, site] }))
  await page.route('**/api/v1/sites/policy-settings-site/traffic', (r) =>
    r.fulfill({ json: { todayBytes: 0 } }),
  )
  await page.route('**/api/v1/sites/policy-settings-site/settings', (r) => {
    submitted = r.request().postDataJSON()
    site.config = submitted.config
    site.revision++
    return r.fulfill({ status: 202, json: { taskId: 'policy-apply' } })
  })
  await page.route('**/api/v1/tasks/policy-apply', (r) =>
    r.fulfill({ json: { state: 'succeeded' } }),
  )
  await page.goto('/sites')
  await page.getByRole('button', { name: 'policies.test', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'policies.test · 网站设置', exact: true })
  await dialog.locator('aside').getByRole('button', { name: '流量限制', exact: true }).click()
  await dialog
    .locator('.el-switch')
    .filter({ has: page.getByRole('switch', { name: '流量限制', exact: true }) })
    .click()
  await dialog.getByRole('button', { name: '保存并应用', exact: true }).click()
  await expect.poll(() => submitted?.revision).toBe(7)
  expect(submitted.config).toMatchObject({
    limitEnabled: true,
    maxConnections: 300,
    perIPConnections: 25,
    rateKB: 512,
  })
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('aside .active')).toHaveText('流量限制')
})

test('managed project runtime users keep compact geometry and do not submit credentials in config', async ({
  page,
}) => {
  let submitted: any
  let headers: Record<string, string> = {}
  await page.route('**/api/v1/resources', (r) => r.fulfill({ json: [nginxFixture] }))
  await page.route('**/api/v1/site-categories', (r) => r.fulfill({ json: { rows: ['默认分类'] } }))
  await page.route('**/api/v1/sites/runtime-users', (r) =>
    r.fulfill({
      json: {
        users: [
          { name: 'www-data', group: 'www-data', uid: '33', gid: '33' },
          { name: 'root', group: 'root', uid: '0', gid: '0' },
          { name: 'deploy', group: 'developers', uid: '1001', gid: '1002' },
        ],
      },
    }),
  )
  await page.route('**/api/v1/sites', (r) => {
    submitted = r.request().postDataJSON()
    headers = r.request().headers()
    return r.fulfill({ status: 202, json: { siteId: 'managed-new', taskId: 'managed-task' } })
  })
  await page.route('**/api/v1/tasks/managed-task', (r) =>
    r.fulfill({ json: { id: 'managed-task', state: 'succeeded' } }),
  )
  await page.goto('/sites')
  await page.getByRole('tab', { name: '其他项目', exact: true }).click()
  await page.getByRole('button', { name: '添加项目', exact: true }).click()
  const dialog = page.getByRole('dialog')
  const geometry = await overlayGeometry(dialog)
  await expect(
    dialog.locator('.el-form-item__label').filter({ hasText: /^SSL 证书$/ }),
  ).toHaveCount(0)
  await expect(dialog.locator('.el-form-item__label').filter({ hasText: /^数据库$/ })).toHaveCount(
    0,
  )
  await expect(dialog.getByRole('checkbox', { name: '放行端口', exact: true })).not.toBeChecked()
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('runtime.example.test')
  await dialog
    .locator('.el-form-item')
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^执行文件$/ }) })
    .getByRole('textbox')
    .fill('/usr/bin/python3')
  await dialog.getByRole('combobox', { name: '运行用户', exact: true }).click()
  await page.getByRole('option', { name: 'root · root', exact: true }).click()
  await expectFixedOverlay(geometry, await overlayGeometry(dialog))
  await dialog
    .locator('label.el-checkbox')
    .filter({ hasText: '确认以 root 运行，并保留目录及 systemd 限制' })
    .click()
  await dialog
    .locator('.el-form-item')
    .filter({ has: page.locator('.el-form-item__label').filter({ hasText: /^管理员密码$/ }) })
    .getByRole('textbox')
    .fill('temporary-admin-verification')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toBeHidden()
  expect(submitted.config.user).toBe('root')
  expect(submitted.config.runtimeIsolation).toBe(true)
  expect(submitted.database).toBeUndefined()
  expect(submitted.certificate).toBeUndefined()
  expect(JSON.stringify(submitted)).not.toContain('temporary-admin-verification')
  expect(headers['x-opsview-password']).toBe('temporary-admin-verification')
})

test('trash cleanup observes the real task and explains retained runtime accounts', async ({
  page,
}) => {
  let finished = false,
    submitted = 0
  const row = {
    id: 'trash-project',
    original: '/www/wwwroot/removed-project',
    deletedAt: '2026-11-22T00:00:00Z',
    expiresAt: '2026-12-22T00:00:00Z',
  }
  await page.route('**/api/v1/files', async (route) => {
    const p = route.request().postDataJSON()
    if (p.action === 'list') return route.fulfill({ json: { path: '/', items: [], total: 0 } })
    if (p.action === 'trash' && p.trashAction === 'list')
      return route.fulfill({ json: { items: finished ? [] : [row] } })
    if (p.action === 'trash' && p.trashAction === 'purge') {
      submitted++
      expect(p.confirmed).toBe(true)
      return route.fulfill({ status: 202, json: { id: 'trash-cleanup', state: 'queued' } })
    }
    return route.fulfill({ json: {} })
  })
  await page.route('**/api/v1/files/tasks/trash-cleanup', (route) =>
    route.fulfill({
      json: finished
        ? {
            state: 'succeeded',
            result: {
              paths: [row.original],
              accounts: [
                {
                  name: 'ovs_retained',
                  group: 'ovs_retained',
                  phase: 'active',
                  reason: '账号仍有外部服务依赖',
                },
              ],
            },
          }
        : { state: 'running' },
    }),
  )
  await page.locator('.sidebar a[href="/files"]').click()
  await page.getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '回收站', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '文件回收站', exact: true })
  await dialog.getByRole('button', { name: '永久删除', exact: true }).click()
  await page
    .locator('.el-message-box')
    .getByRole('button', { name: '永久删除', exact: true })
    .click()
  await expect(dialog).toContainText('正在处理回收站及运行用户依赖')
  await expect(dialog.getByRole('button', { name: '永久删除', exact: true })).toBeDisabled()
  expect(submitted).toBe(1)
  finished = true
  await expect(
    page.getByText('文件已处理，保留运行用户：ovs_retained（账号仍有外部服务依赖）', {
      exact: true,
    }),
  ).toBeVisible()
  await expect(dialog).not.toContainText(row.original)
})
