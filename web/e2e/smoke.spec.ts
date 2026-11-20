import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  const r = await page.request.post('/auth/break-glass', { data: { password: 'e2e-password' } })
  expect(r.status()).toBe(204)
})

test('shell renders after break-glass login and deep link reloads (UI-005)', async ({ page }) => {
  await page.goto('/audit')
  await expect(page.getByRole('heading', { name: 'Audit' })).toBeVisible()
  await expect(page.getByText('Break-glass session')).toBeVisible()
  await expect(page.locator('.conn')).toHaveText(/Live/)
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Audit' })).toBeVisible()
})

test('unauthenticated visit redirects to /login', async ({ browser, baseURL }) => {
  const ctx = await browser.newContext({ baseURL })
  const page = await ctx.newPage()
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await ctx.close()
})

test('stream outage shows the stale banner and recovery clears it (UI-023)', async ({ page }) => {
  await page.route('**/api/v1/events*', (r) => r.abort())
  await page.goto('/')
  await expect(page.getByText(/Live updates paused/)).toBeVisible({ timeout: 15000 })
  await page.unroute('**/api/v1/events*')
  await expect(page.getByText(/Live updates paused/)).toBeHidden({ timeout: 15000 })
  await expect(page.locator('.conn')).toHaveText(/Live/)
})

test('axe finds no serious violations on the shell (UI-033)', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  const { violations } = await new AxeBuilder({ page }).analyze()
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
})
