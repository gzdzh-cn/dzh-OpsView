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
  await page.goto('/sites')
  await page.getByRole('button', { name: '添加网站' }).first().click()
  await page.getByPlaceholder('字母、数字、横线或下划线，例如 my-app').fill('e2e-invalid-site')
  await page
    .getByPlaceholder('example.com www.example.com，以空格分隔')
    .fill('example.com;include /etc/passwd')
  await page.getByRole('button', { name: '创建并部署' }).click()
  await expect(page.locator('.el-dialog .el-alert')).toBeVisible()
  await expect(page.locator('.el-dialog')).toBeVisible()
})
test('queued installation reports executor unavailable instead of success', async ({ page }) => {
  await page.goto('/software')
  await page
    .locator('.software-card')
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
  await expect(page.getByRole('heading', { name: '面板配置备份', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: backups[0].name })).toBeVisible()
})
