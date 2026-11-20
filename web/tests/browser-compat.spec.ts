import { test, expect, type Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
const username = 'e2e-admin',
  password = 'OpsView-e2e-only-Password-2026!'
test.use({
  launchOptions: {
    args: ['--host-resolver-rules=MAP opsview-http.test 127.0.0.1', '--no-proxy-server'],
  },
})
test.beforeAll(async ({ request }) => {
  const status = await (await request.get('/api/v1/auth')).json()
  if (status.setupRequired) {
    const token = fs
      .readFileSync(path.join(process.env.OPSVIEW_E2E_DATA!, 'setup.token'), 'utf8')
      .trim()
    expect(
      (await request.post('/api/v1/auth/setup', { data: { token, username, password } })).status(),
    ).toBe(201)
  }
})
const nginx = {
  id: 'http-nginx',
  kind: 'instance',
  name: 'nginx',
  status: 'ready',
  revision: 1,
  config: { software: 'nginx', mode: 'native', version: '1.30.6' },
}
async function loginHTTP(page: Page, baseURL: string) {
  const address = new URL(baseURL)
  address.hostname = 'opsview-http.test'
  await page.goto(address.toString())
  expect(await page.evaluate(() => isSecureContext)).toBe(false)
  expect(await page.evaluate(() => typeof crypto.randomUUID)).toBe('undefined')
  await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
  await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '进入控制台' }).click()
  await expect(page.getByRole('main', { name: '概览', exact: true })).toBeVisible()
}
for (const kind of ['php', 'proxy', 'managed'])
  test(`public HTTP creates ${kind} sites with a stable compatible UUID`, async ({
    page,
    baseURL,
  }) => {
    const errors: string[] = []
    page.on('pageerror', (e) => errors.push(e.message))
    let body: any,
      posts = 0
    await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginx] }))
    await page.route('**/api/v1/sites/provisions/*', (route) =>
      route.fulfill({ status: 404, json: { error: 'not found' } }),
    )
    await page.route('**/api/v1/sites', (route) => {
      posts++
      body = route.request().postDataJSON()
      return route.fulfill({ status: 202, json: { siteId: 'http-site', taskId: 'http-create' } })
    })
    await page.route('**/api/v1/tasks/http-create', (route) =>
      route.fulfill({ json: { id: 'http-create', state: 'succeeded' } }),
    )
    await loginHTTP(page, baseURL!)
    await page.locator('.sidebar a[href="/sites"]').click()
    if (kind === 'proxy') await page.getByRole('tab', { name: '反向代理', exact: true }).click()
    if (kind === 'managed') await page.getByRole('tab', { name: '其他项目', exact: true }).click()
    await page
      .getByRole('button', {
        name: kind === 'php' ? '添加PHP 项目' : kind === 'proxy' ? '添加反向代理' : '添加项目',
        exact: true,
      })
      .click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeEnabled()
    await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill(`${kind}.http.test`)
    if (kind === 'managed')
      await dialog.getByPlaceholder('绝对路径，例如 /usr/bin/node').fill('/usr/bin/node')
    await dialog.getByRole('button', { name: '创建', exact: true }).dblclick()
    await expect(dialog).toBeHidden()
    expect(posts).toBe(1)
    expect(body.requestId).toMatch(
      /^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/,
    )
    expect(body.config.type).toBe(kind === 'php' ? 'static' : kind)
    expect(
      await page.evaluate(() => sessionStorage.getItem('opsview:site-creation-request')),
    ).toBeNull()
    expect(errors).toEqual([])
  })

test('HTTP creation distinguishes query errors, recovers lost replies and resumes across reload', async ({
  page,
  baseURL,
}) => {
  let posts = 0,
    queryError = true,
    disconnected = true
  const ids = new Set<string>()
  const site = {
    id: 'resume-http',
    kind: 'site',
    name: 'resume.http.test',
    status: 'pending',
    revision: 1,
    config: {
      type: 'static',
      domains: 'resume.http.test',
      instanceId: nginx.id,
      rootDirectory: '/www/wwwroot/resume.http.test',
    },
  }
  const progress = () => ({
    siteId: site.id,
    taskId: 'resume-task',
    state: 'running',
    revision: 1,
    databaseReady: true,
  })
  await page.route('**/api/v1/resources', (route) =>
    route.fulfill({ json: posts ? [nginx, site] : [nginx] }),
  )
  await page.route('**/api/v1/sites/provisions/*', (route) => {
    ids.add(new URL(route.request().url()).pathname.split('/').at(-1)!)
    return queryError
      ? route.fulfill({ status: 500, json: { error: '创建查询暂时不可用' } })
      : posts
        ? route.fulfill({ json: progress() })
        : route.fulfill({ status: 404, json: { error: 'not found' } })
  })
  await page.route('**/api/v1/sites', (route) => {
    posts++
    return route.abort('connectionreset')
  })
  await page.route('**/api/v1/tasks/resume-task', (route) =>
    disconnected
      ? route.fulfill({ status: 503, json: { error: 'offline' } })
      : route.fulfill({ json: { id: 'resume-task', state: 'succeeded' } }),
  )
  await loginHTTP(page, baseURL!)
  await page.locator('.sidebar a[href="/sites"]').click()
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  let dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeEnabled()
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill(site.name)
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toContainText('创建查询暂时不可用')
  expect(posts).toBe(0)
  queryError = false
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toContainText('后台任务未取消', { timeout: 15000 })
  expect(posts).toBe(1)
  expect(await page.evaluate(() => sessionStorage.getItem('opsview:site-creation-request'))).toBe(
    [...ids][0],
  )
  // A real reload loses all component state, but must retain the original request identity.
  await page.reload()
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('textbox', { name: '绑定域名', exact: true })).toHaveValue(
    site.name,
  )
  await expect(dialog).toContainText('关联数据库已创建')
  disconnected = false
  await expect(dialog).toBeHidden({ timeout: 10000 })
  expect(posts).toBe(1)
  expect(ids.size).toBe(1)
})

test('HTTP credential copying offers manual selection and Passkey explains HTTPS requirements', async ({
  page,
  baseURL,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(document, 'execCommand', { value: () => false, configurable: true })
  })
  await page.route('**/api/v1/auth/tokens/api', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({ json: { token: 'test-one-time-token' } })
      : route.fulfill({ json: [] }),
  )
  await page.route('**/api/v1/auth/passkeys/list', (route) => route.fulfill({ json: { rows: [] } }))
  await loginHTTP(page, baseURL!)
  expect(await page.evaluate(() => typeof navigator.clipboard)).toBe('undefined')
  await page.locator('.sidebar a[href="/settings"]').click()
  await page.getByRole('tab', { name: '安全设置', exact: true }).click()
  await page.getByRole('button', { name: '修改API 凭据', exact: true }).click()
  await page.getByRole('button', { name: '创建凭据', exact: true }).click()
  await page.getByRole('button', { name: '验证并创建', exact: true }).click()
  const verify = page.getByRole('dialog', { name: '验证管理员身份', exact: true })
  await verify.getByRole('textbox').fill(password)
  await verify.getByRole('button', { name: '验证', exact: true }).click()
  await expect(page.getByRole('textbox', { name: '新授权令牌', exact: true })).toHaveValue(
    'test-one-time-token',
  )
  await page.getByRole('button', { name: '复制', exact: true }).click()
  const manual = page.getByRole('dialog', { name: '手动复制', exact: true })
  await expect(manual.getByRole('textbox', { name: '手动复制内容' })).toHaveValue(
    'test-one-time-token',
  )
  await expect(manual.getByRole('textbox')).toHaveAttribute('readonly', '')
  await page.evaluate(() => window.dispatchEvent(new Event('opsview:session-cleared')))
  await expect(manual).toBeHidden()
  await page.getByRole('button', { name: '修改Passkey', exact: true }).click()
  await expect(page.getByRole('button', { name: '绑定 Passkey', exact: true })).toBeDisabled()
  await expect(page.getByRole('alert').filter({ hasText: 'Passkey 需要通过 HTTPS' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '新授权令牌' })).toHaveCount(0)
})

test('unsupported secure randomness stops creation with an actionable error', async ({
  page,
  baseURL,
}) => {
  let posts = 0
  await page.addInitScript(() => {
    Object.defineProperty(crypto, 'getRandomValues', { value: undefined })
  })
  await page.route('**/api/v1/resources', (route) => route.fulfill({ json: [nginx] }))
  await page.route('**/api/v1/sites', (route) => {
    posts++
    return route.fulfill({ json: {} })
  })
  await loginHTTP(page, baseURL!)
  await page.locator('.sidebar a[href="/sites"]').click()
  await page.getByRole('button', { name: '添加PHP 项目', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeEnabled()
  await dialog.getByRole('textbox', { name: '绑定域名', exact: true }).fill('unsupported.test')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(dialog).toContainText('无法生成安全随机数')
  await expect(dialog.getByRole('button', { name: '创建', exact: true })).toBeEnabled()
  expect(posts).toBe(0)
})

test('task logs reject malformed frames and stay isolated when changing selection', async ({
  page,
  baseURL,
}) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  const tasks = ['one', 'two'].map((id) => ({
    id,
    operation: `fixture-${id}`,
    state: 'running',
    createdAt: new Date().toISOString(),
  }))
  await page.route('**/api/v1/tasks', (route) => route.fulfill({ json: tasks }))
  await page.route('**/api/v1/tasks/*/events**', (route) => {
    if (!route.request().url().includes('stream=true')) return route.fulfill({ json: [] })
    const id = route.request().url().includes('/one/') ? 'one' : 'two'
    return route.fulfill({
      contentType: 'text/event-stream',
      body:
        (id === 'one' ? 'data: malformed\n\n' : '') +
        'id: 1\ndata: ' +
        JSON.stringify({
          sequence: 1,
          message: `${id}-only`,
          createdAt: new Date().toISOString(),
        }) +
        '\n\n',
    })
  })
  await loginHTTP(page, baseURL!)
  await page.locator('.sidebar a[href="/tasks"]').click()
  await page.getByText('fixture-one', { exact: true }).click()
  let drawer = page.getByRole('dialog', { name: '任务执行详情', exact: true })
  await expect(drawer).toContainText('日志数据格式异常')
  await expect(drawer.locator('.log-box')).toContainText('one-only')
  await drawer.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByText('fixture-two', { exact: true }).click()
  drawer = page.getByRole('dialog', { name: '任务执行详情', exact: true })
  await expect(drawer.locator('.log-box')).toContainText('two-only')
  await expect(drawer.locator('.log-box')).not.toContainText('one-only')
  expect(errors).toEqual([])
})
