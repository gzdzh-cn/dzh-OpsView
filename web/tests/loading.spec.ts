import { test, expect, type Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'

const username = 'e2e-admin',
  password = 'OpsView-e2e-only-Password-2026!'
const resources = [
  {
    id: 'loading-nginx',
    kind: 'instance',
    name: 'Nginx',
    status: 'ready',
    config: { software: 'nginx', version: '1.28.0' },
  },
  {
    id: 'loading-site',
    kind: 'site',
    name: 'loading.example.test',
    status: 'ready',
    config: {
      type: 'static',
      domains: 'loading.example.test',
      root: '/www/wwwroot/loading.example.test',
    },
  },
]
function gate() {
  let release!: () => void
  const promise = new Promise<void>((resolve) => {
    release = resolve
  })
  return { promise, release }
}
async function login(page: Page) {
  await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
  await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
  await page.getByRole('button', { name: '进入控制台', exact: true }).click()
}
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
test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/resources', (r) => r.fulfill({ json: resources }))
  await page.route('**/api/v1/sites/loading-site/traffic**', (r) => r.fulfill({ json: {} }))
  await page.goto('/')
  await login(page)
  await expect(page.getByRole('main', { name: '概览', exact: true })).toBeVisible()
})

test('same-route login immediately shows loading, never missing Nginx, without a duplicate auth read', async ({
  page,
}) => {
  await page.locator('.sidebar a[href="/sites"]').click()
  await expect(
    page.getByRole('button', { name: 'loading.example.test', exact: true }),
  ).toBeVisible()
  await page.locator('.sidebar-account button').hover()
  await page.getByRole('menuitem', { name: '退出登录', exact: true }).click()
  await expect(page.getByRole('button', { name: '进入控制台', exact: true })).toBeVisible()
  const data = gate(),
    prefs = gate()
  let authReads = 0
  await page.route('**/api/v1/auth', async (r) => {
    authReads++
    await new Promise((resolve) => setTimeout(resolve, 350))
    await r.continue()
  })
  await page.route('**/api/v1/resources', async (r) => {
    await Promise.all([data.promise, new Promise((resolve) => setTimeout(resolve, 2000))])
    await r.fulfill({ json: resources }).catch(() => {})
  })
  for (const endpoint of ['settings', 'public/preferences']) {
    await page.route(`**/api/v1/${endpoint}`, async (r) => {
      await prefs.promise
      await r.continue().catch(() => {})
    })
  }
  await page.evaluate(() => {
    const state = { missing: false, firstFrameBusy: false, seen: false }
    ;(window as any).__loginLoading = state
    const observer = new MutationObserver(() => {
      if (document.querySelector('.nginx-required')) state.missing = true
      const main = document.querySelector('main[aria-label="网站"]')
      if (main && !state.seen) {
        state.seen = true
        state.firstFrameBusy = !!main.querySelector('.loading-content[aria-busy="true"]')
      }
    })
    observer.observe(document.body, { childList: true, subtree: true, attributes: true })
  })
  try {
    await login(page)
    await expect(page.getByRole('main', { name: '网站', exact: true })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'PHP 项目', exact: true })).toBeVisible()
    await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toBeVisible()
    expect(await page.evaluate(() => (window as any).__loginLoading)).toMatchObject({
      missing: false,
      firstFrameBusy: true,
      seen: true,
    })
    expect(authReads).toBe(1)
    data.release()
    await expect(
      page.getByRole('button', { name: 'loading.example.test', exact: true }),
    ).toBeVisible()
    expect(await page.evaluate(() => (window as any).__loginLoading.missing)).toBe(false)
  } finally {
    data.release()
    prefs.release()
  }
})

test('all cold menus show a local spinner with stable navigation on slow interfaces', async ({
  page,
}) => {
  await page.addInitScript(() => {
    const state = { mainAt: 0, loadingAt: 0 }
    ;(window as any).__loadingTiming = state
    new MutationObserver(() => {
      const main = document.querySelector('main')
      if (!main) return
      if (!state.mainAt) state.mainAt = performance.now()
      if (!state.loadingAt && main.querySelector('.loading-content[aria-busy="true"]'))
        state.loadingAt = performance.now()
    }).observe(document, { childList: true, subtree: true, attributes: true })
  })
  let current = gate()
  const delayed = new Set([
    'resources',
    'tasks',
    'catalog',
    'overview',
    'docker/status',
    'settings',
    'settings/runtime',
    'schedules',
    'system/firewall',
    'files',
    'plugins',
  ])
  await page.route('**/api/v1/**', async (r) => {
    const endpoint = new URL(r.request().url()).pathname.replace('/api/v1/', '')
    if (delayed.has(endpoint)) await current.promise
    await r.fallback().catch(() => {})
  })
  try {
    for (const [url, title] of [
      ['/', '概览'],
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
      current = gate()
      await page.goto(url!)
      await expect(page.getByRole('main', { name: title!, exact: true })).toBeVisible()
      await expect(
        page.locator('.page-content .loading-content[aria-busy="true"]').first(),
      ).toBeVisible()
      await expect(
        page.locator(
          '.nginx-required, .engine-message, .page-content .el-empty, .page-content .el-table__empty-text',
        ),
      ).toHaveCount(0)
      if (url === '/files')
        await expect(page.getByRole('navigation', { name: '目录路径', exact: true })).toBeVisible()
      else if (!['/', '/tasks'].includes(url!))
        await expect(page.getByRole('tab').first()).toBeVisible()
      const timing = await page.evaluate(() => (window as any).__loadingTiming)
      expect(timing.loadingAt - timing.mainAt, `${title} feedback`).toBeLessThanOrEqual(200)
      await page.screenshot({
        path: `../.opsview/screenshots/loading-${title}.png`,
        animations: 'disabled',
      })
      current.release()
    }
  } finally {
    current.release()
  }
})

test('cold resource failure offers retry before a genuine empty result can show installation guidance', async ({
  page,
}) => {
  let fail = true
  await page.route('**/api/v1/resources', (r) =>
    fail
      ? r.fulfill({ status: 503, json: { error: '资源读取暂不可用' } })
      : r.fulfill({ json: [] }),
  )
  await page.goto('/sites')
  const error = page.locator('.page-alert').filter({ hasText: '资源读取暂不可用' })
  await expect(error).toBeVisible()
  await expect(page.locator('.nginx-required')).toHaveCount(0)
  await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toHaveCount(0)
  fail = false
  await error.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.locator('.nginx-required')).toBeVisible()
})

test('cold settings errors never display default values and allow a successful retry', async ({
  page,
}) => {
  let fail = true
  await page.route('**/api/v1/settings', (r) =>
    fail ? r.fulfill({ status: 503, json: { error: '设置读取暂不可用' } }) : r.continue(),
  )
  await page.goto('/settings')
  await expect(page.getByRole('tab', { name: '常用设置', exact: true })).toBeVisible()
  await expect(page.getByText('设置读取暂不可用', { exact: true })).toBeVisible()
  await expect(page.locator('.settings-surface')).toHaveCount(0)
  fail = false
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.locator('.settings-surface')).toBeVisible()
  await expect(page.locator('.preference-item').first()).toBeVisible()
})

test('warm menu switches retain content and background resource failures never show a missing dependency', async ({
  page,
}) => {
  await page.goto('/sites')
  await expect(
    page.getByRole('button', { name: 'loading.example.test', exact: true }),
  ).toBeVisible()
  await page.locator('.sidebar a[href="/databases"]').click()
  await expect(page.getByRole('tab', { name: 'MySQL', exact: true })).toBeVisible()
  await page.locator('.sidebar a[href="/sites"]').click()
  await expect(
    page.getByRole('button', { name: 'loading.example.test', exact: true }),
  ).toBeVisible()
  await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toHaveCount(0)
  await page.route('**/api/v1/resources', (r) =>
    r.fulfill({ status: 503, json: { error: '后台更新失败' } }),
  )
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  await expect(page.getByText('后台更新失败', { exact: true })).toBeVisible()
  await expect(
    page.getByRole('button', { name: 'loading.example.test', exact: true }),
  ).toBeVisible()
  await expect(page.locator('.nginx-required')).toHaveCount(0)
})

for (const theme of ['light', 'dark']) {
  test(`mobile ${theme} loading keeps tabs and navigation usable`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.route('**/api/v1/public/preferences', (r) => r.fulfill({ json: { theme } }))
    await page.route('**/api/v1/settings', (r) =>
      r.fulfill({ json: { theme, refreshSeconds: 15 } }),
    )
    const wait = gate()
    for (const endpoint of ['resources', 'docker/status']) {
      await page.route(`**/api/v1/${endpoint}`, async (r) => {
        await wait.promise
        await r.fallback().catch(() => {})
      })
    }
    try {
      await page.goto('/sites')
      await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toBeVisible()
      await expect(page.getByRole('tab', { name: 'PHP 项目', exact: true })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(
        theme === 'dark',
      )
      await page.getByRole('button', { name: '打开导航菜单', exact: true }).click()
      await page.locator('.sidebar a[href="/docker"]').click()
      await expect(page.getByRole('main', { name: 'Docker', exact: true })).toBeVisible()
      await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toBeVisible()
      const tabs = page.getByRole('tab', { name: '镜像', exact: true })
      await tabs.click()
      const before = await tabs.evaluate((el) => el.getBoundingClientRect().y)
      await page.screenshot({
        path: `../.opsview/screenshots/loading-mobile-${theme}.png`,
        animations: 'disabled',
      })
      wait.release()
      await expect(page.locator('.page-content .loading-content[aria-busy="true"]')).toHaveCount(0)
      expect(
        Math.abs((await tabs.evaluate((el) => el.getBoundingClientRect().y)) - before),
      ).toBeLessThanOrEqual(1)
    } finally {
      wait.release()
    }
  })
}

test('local read failures offer retry without fake SSH status or empty notification records', async ({
  page,
}) => {
  let failed = true
  await page.route('**/api/v1/system/ssh', (r) =>
    failed
      ? r.fulfill({ status: 503, json: { error: 'SSH读取暂不可用' } })
      : r.fulfill({
          json: { editable: false, service: { running: true }, reason: '测试只读诊断' },
        }),
  )
  await page.goto('/system')
  await page.getByRole('tab', { name: 'SSH 管理', exact: true }).click()
  await expect(page.getByText('SSH读取暂不可用', { exact: true })).toBeVisible()
  await expect(page.getByText('只读诊断', { exact: true })).toHaveCount(0)
  await expect(page.locator('.page-content .data-list')).toHaveCount(0)
  failed = false
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByText('测试只读诊断', { exact: true })).toBeVisible()

  failed = true
  await page.route('**/api/v1/notifications/rules', (r) =>
    failed
      ? r.fulfill({ status: 503, json: { error: '告警读取暂不可用' } })
      : r.fulfill({ json: [] }),
  )
  await page.locator('.sidebar a[href="/settings"]').click()
  await page.getByRole('tab', { name: '告警通知', exact: true }).click()
  await expect(page.getByText('告警读取暂不可用', { exact: true })).toBeVisible()
  await expect(page.locator('.notification-manager .data-list')).toHaveCount(0)
  failed = false
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.locator('.notification-manager .data-list')).toBeVisible()
})
