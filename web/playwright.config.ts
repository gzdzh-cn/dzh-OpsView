import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const panelCommand =
  process.env.OPSVIEW_E2E_RELEASE === 'true'
    ? '../dist/opsview-linux-amd64/bin/opsview'
    : 'go run ./cmd/opsview'
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  timeout: 30000,
  use: { baseURL: 'http://127.0.0.1:18890', headless: true, trace: 'retain-on-failure' },
  webServer: {
    command: `${panelCommand} --dev --listen 127.0.0.1:18890 --data ../.opsview/e2e`,
    cwd: path.join(root, 'server'),
    url: 'http://127.0.0.1:18890/api/v1/health',
    reuseExistingServer: false,
    timeout: 120000,
  },
})
