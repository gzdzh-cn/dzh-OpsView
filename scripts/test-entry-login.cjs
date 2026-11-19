// Isolated Linux CI only. Credentials arrive via stdin and never enter logs/argv.
const fs = require('node:fs')
const { execFileSync } = require('node:child_process')
const { createRequire } = require('node:module')
const { resolve } = require('node:path')
const { chromium } = createRequire(resolve(__dirname, '../web/package.json'))('playwright')
let phase = 'startup'
async function main() {
  if (process.env.GITHUB_ACTIONS !== 'true' || process.getuid() !== 0) throw Error('isolated CI required')
  const { username, password, port } = JSON.parse(fs.readFileSync(0, 'utf8'))
  const cfg = () => JSON.parse(fs.readFileSync('/etc/opsview/panel.json', 'utf8'))
  const browser = await chromium.launch({ headless: true })
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true })
    const page = await context.newPage()
    async function connect(scheme, token) {
      // Access changes schedule a restart after three seconds; wait for the
      // requested listener instead of racing the still-running old protocol.
      phase = scheme + '-wait-listener'
      const deadline = Date.now() + 30000
      let ready = false
      while (Date.now() < deadline) {
        try {
          const response = await context.request.get(`${scheme}://127.0.0.1:${port}/api/v1/health`, { timeout: 2000 })
          ready = response.ok() && (await response.json()).status === 'ok'
          await response.dispose()
          if (ready) break
        } catch {}
        await new Promise(resolve => setTimeout(resolve, 250))
      }
      if (!ready) throw Error('listener did not become ready')
      phase = scheme + '-open-entrance'
      const entry = cfg().entry
      await page.goto(`${scheme}://127.0.0.1:${port}/${entry}${token ? '#runtime-confirm=' + token : ''}`)
      const login = page.getByRole('button', { name: '进入控制台' })
      // Wait for initialization before choosing the login path.
      await page.waitForFunction(() => document.querySelector('main') || document.querySelector('.login-form form'))
      phase = scheme + '-login'
      if (await login.isVisible()) {
        await page.getByRole('textbox', { name: '用户名', exact: true }).fill(username)
        await page.getByRole('textbox', { name: '密码', exact: true }).fill(password)
        await login.click()
      }
      await page.getByRole('main', { name: '概览', exact: true }).waitFor()
      await page.waitForURL(url => !url.hash.includes('runtime-confirm'))
      phase = scheme + '-session'
      const auth = await page.evaluate(async () => (await fetch('/api/v1/auth')).json())
      if (!auth.authenticated) throw Error('session not saved')
      phase = scheme + '-reload'
      await page.reload()
      await page.getByRole('main', { name: '概览', exact: true }).waitFor()
    }
    await connect('https')
    for (const [mode, scheme] of [['off', 'http'], ['on', 'https']]) {
      phase = 'ssl-' + mode
      const output = execFileSync('ov', ['ssl', mode, '--yes'], { encoding: 'utf8', timeout: 30000 })
      const token = output.match(/#runtime-confirm=([a-f0-9]{64})/)?.[1]
      if (!token) throw Error('missing reconnect token')
      await connect(scheme, token)
    }
    const cookies = await context.cookies()
    if (!cookies.some(c => c.name === 'opsview_entry' && c.secure) || !cookies.some(c => c.name === 'opsview_entry_http' && !c.secure)) throw Error('protocol cookies missing')
  } finally { await browser.close() }
}
main().catch(() => { console.error('OPSVIEW_BROWSER_PHASE=' + phase); process.exitCode = 1 })
