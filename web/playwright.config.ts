import { defineConfig } from '@playwright/test'

// Runs the real binary (make build first) with a break-glass password; no cluster needed for the shell smoke.
export default defineConfig({
  testDir: 'e2e',
  timeout: 60000,
  use: { baseURL: 'http://127.0.0.1:8123' },
  webServer: {
    command: 'sh e2e/serve.sh',
    url: 'http://127.0.0.1:8123/healthz',
    timeout: 30000,
    reuseExistingServer: false,
  },
})
