import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'

const username = 'e2e-admin'
const password = 'OpsView-e2e-only-Password-2026!'

// A real non-loopback host is needed: browsers grant localhost special cookie
// exceptions that hide the Secure-cookie collision seen on public IP addresses.
test.use({ launchOptions: { args: ['--host-resolver-rules=MAP opsview-cookie.test 127.0.0.1', '--no-proxy-server'] } })

test.beforeAll(async ({ request }) => {
  const status = await (await request.get('/api/v1/auth')).json()
  if (status.setupRequired) {
    const token = fs.readFileSync(path.join(process.env.OPSVIEW_E2E_DATA!, 'setup.token'), 'utf8').trim()
    const response = await request.post('/api/v1/auth/setup', { data: { token, username, password } })
    expect(response.status()).toBe(201)
  }
})

test('HTTP login works with an existing Secure HTTPS cookie and survives reload', async ({ page, context, baseURL }) => {
  const address = new URL(baseURL!)
  address.hostname = 'opsview-cookie.test'
  await context.addCookies([{
    name: 'opsview_session', value: 'old-https-session-fixture', domain: address.hostname,
    path: '/', secure: true, httpOnly: true, sameSite: 'Strict',
  }])
  await page.goto(address.toString())
  await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
  await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '进入控制台' }).click()
  await expect(page.getByRole('heading', { name: '概览', exact: true })).toBeVisible()
  const cookies = await context.cookies()
  expect(cookies.find(c => c.name === 'opsview_session')?.value).toBe('old-https-session-fixture')
  const session = cookies.find(c => c.name === 'opsview_session_http')
  expect(session?.secure).toBe(false)
  expect(session?.httpOnly).toBe(true)
  await page.reload()
  await expect(page.getByRole('heading', { name: '概览', exact: true })).toBeVisible()
  await page.locator('.sidebar a[href="/sites"]').click()
  await expect(page.getByRole('heading', { name: '网站', exact: true, level: 1 })).toBeVisible()
})

test('blocked session cookies show a login error instead of silently returning', async ({ page, playwright }) => {
  // Use an independent API context: route.fetch() shares the browser cookie jar
  // and would store Set-Cookie before the simulated browser rejection.
  const upstream = await playwright.request.newContext()
  await page.route('**/api/v1/auth/login', async route => {
    const response = await upstream.fetch(route.request())
    expect(response.status()).toBe(200)
    await route.fulfill({ status: response.status(), json: await response.json() })
  })
  try {
    await page.goto('/')
    await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
    await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
    await page.getByRole('button', { name: '进入控制台' }).click()
    await expect(page.getByRole('alert')).toContainText('浏览器未保存登录会话')
    await expect(page.getByRole('heading', { name: '概览', exact: true })).toHaveCount(0)
  } finally {
    await upstream.dispose()
  }
})
