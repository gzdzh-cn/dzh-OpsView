import { defineConfig } from '@playwright/test'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  timeout: 30000,
  use: { baseURL: 'http://127.0.0.1:18890', headless: true, trace: 'retain-on-failure' },
  webServer: {
    command:
      'go run ./cmd/opsview --dev --listen 127.0.0.1:18890 --data ../.opsview/e2e --web ../web/dist',
    cwd: path.join(root, 'server'),
    url: 'http://127.0.0.1:18890/api/v1/health',
    reuseExistingServer: false,
    timeout: 120000,
  },
})
