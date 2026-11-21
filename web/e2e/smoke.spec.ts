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

for (const colorScheme of ['light', 'dark'] as const) {
  test(`sign-in page renders and passes axe in ${colorScheme} theme (UI-033)`, async ({ browser, baseURL }) => {
    const c = await browser.newContext({ baseURL, colorScheme })
    const page = await c.newPage()
    await page.goto('/login')
    await expect(page.getByRole('link', { name: 'Sign in with OIDC' })).toHaveAttribute('href', '/auth/login')
    await expect(page.getByLabel('Break-glass password')).toBeVisible()
    const { violations } = await new AxeBuilder({ page }).analyze()
    expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
    await c.close()
  })
}

// Every theme the switcher offers must pass axe (D-125). The choice is persisted in localStorage and applied as data-theme.
for (const theme of ['light', 'dark', 'corporate', 'business', 'nord']) {
  test(`axe passes on the shell and sign-in in the ${theme} theme (UI-033)`, async ({ page, browser, baseURL }) => {
    await page.addInitScript((t) => localStorage.setItem('c8:theme', t), theme)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    await page.getByRole('button', { name: 'Choose theme' }).focus()
    await page.getByRole('button', { name: /Break-glass admin/ }).focus()
    await page.waitForTimeout(500) // dropdown fades in; axe must not sample mid-transition
    const { violations } = await new AxeBuilder({ page }).analyze()
    expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
    const c = await browser.newContext({ baseURL })
    const login = await c.newPage()
    await login.addInitScript((t) => localStorage.setItem('c8:theme', t), theme)
    await login.goto('/login')
    await expect(login.getByLabel('Break-glass password')).toBeVisible()
    const r = await new AxeBuilder({ page: login }).analyze()
    expect(r.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
    await c.close()
  })
}

test('phone width: drawer opens from the hamburger, dock is shown, no horizontal page scroll', async ({ browser, baseURL }) => {
  const c = await browser.newContext({ baseURL, viewport: { width: 375, height: 700 } })
  const page = await c.newPage()
  expect((await page.request.post('/auth/break-glass', { data: { password: 'e2e-password' } })).status()).toBe(204)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Quick navigation' })).toBeVisible()
  await page.getByRole('button', { name: 'Open navigation' }).click()
  await expect(page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Audit' })).toBeVisible()
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Audit' }).click()
  await expect(page.getByRole('heading', { name: 'Audit' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await c.close()
})
