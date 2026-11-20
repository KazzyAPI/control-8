import { defineConfig } from '@playwright/test'

// Runs the real binary (make build first) with a break-glass password; no cluster needed for the shell smoke.
export default defineConfig({
  testDir: 'e2e',
  timeout: 60000,
  workers: 1, // one shared server and DB
  // C8_CHROMIUM lets an offline machine point at an already-installed Chromium.
  use: { baseURL: 'http://127.0.0.1:8123', launchOptions: { executablePath: process.env.C8_CHROMIUM || undefined } },
  webServer: {
    command: 'sh e2e/serve.sh',
    url: 'http://127.0.0.1:8123/healthz',
    timeout: 30000,
    reuseExistingServer: false,
  },
})
