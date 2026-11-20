import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

// M14 read screens against the real Go binary. No cluster: the stack/revision/deployment payloads are mocked at the
// network edge (scale/rollout scenarios on kind are not run here).
const stack = (ready: number, name = 'api') => ({ id: 's1', namespace: 'shop', slug: 'web', asset: 'web', target: 'prod-eu', runtime: 'kubernetes',
  environment_tier: 'prod', state: 'active', safety_profile: 'managed_rollout', health: 'healthy', desired_revision: 1,
  services: [{ kind: 'Deployment', name, desired_replicas: 3, ready_replicas: ready, image_ref: 'r/api:1', image_id: 'sha256:abc' }] })
const dep = (status: string, extra = {}) => ({ id: 'd1', namespace: 'shop', stack: 'web', action: 'apply', revision: 1, runtime: 'kubernetes', status,
  safety_profile: 'managed_rollout', environment_tier: 'prod', requested_by: 'ann',
  rollout: { state: 'waiting', workloads: [{ kind: 'Deployment', name: 'api', desired: 3, updated: 1, ready: 1, available: 1 }] }, ...extra })
const json = (body: unknown) => ({ contentType: 'application/json', body: JSON.stringify(body) })
const sse = (type: string, ns: string, data: unknown, delay = 800) => async (r: any) => {
  await new Promise((x) => setTimeout(x, delay))
  await r.fulfill({ contentType: 'text/event-stream', body: `retry: 60000\nid: 1\nevent: ${type}\ndata: ${JSON.stringify({ type, at: new Date().toISOString(), namespace: ns, data })}\n\n` })
}

async function mockStack(page: Page, s: unknown) {
  await page.route('**/api/v1/namespaces/shop/stacks/web', (r) => r.fulfill(json(s)))
  await page.route('**/api/v1/namespaces/shop/stacks/web/deployments', (r) => r.fulfill(json({ items: [dep('running')] })))
  await page.route('**/api/v1/namespaces/shop/stacks/web/revisions', (r) => r.fulfill(json({ items: [{ number: 1, safety_profile: 'managed_rollout' }] })))
  await page.route('**/api/v1/namespaces/shop/stacks/web/revisions/1', (r) => r.fulfill(json({ number: 1, safety_profile: 'managed_rollout', origin: 'user',
    policy_result: { allowed: true }, inventory: [{ kind: 'Deployment', name: 'api' }], manifests_input: 'kind: Deployment', created_by: 'ann' })))
  await page.route('**/api/v1/deployments/d1', (r) => r.fulfill(json(dep('running', { output: 'applying' }))))
}
const axe = async (page: Page) => {
  const { violations } = await new AxeBuilder({ page }).analyze()
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
}

test.beforeEach(async ({ page }) => {
  expect((await page.request.post('/auth/break-glass', { data: { password: 'e2e-password' } })).status()).toBe(204)
})

test('UI-033 axe passes on every read screen; real namespace and asset', async ({ page }) => {
  const csrf = (await (await page.request.get('/api/v1/me')).json()).csrf_token
  const h = { 'X-CSRF-Token': csrf }
  await page.request.post('/api/v1/namespaces', { headers: h, data: { slug: 'shop', display_name: 'Shop', environment_tier: 'prod', approver_group: 'shop-approvers' } })
  await page.request.post('/api/v1/namespaces/shop/assets', { headers: h, data: { slug: 'web', name: 'Web', managed_by: 'control8' } })
  await mockStack(page, stack(3))
  const routes: [string, string][] = [['/', 'Overview'], ['/n/shop', 'Shop'], ['/n/shop/assets/web', 'Web'], ['/n/shop/s/web', 'web'],
    ['/n/shop/s/web/revisions/1', 'Revision 1'], ['/deployments/d1', 'Deployment d1'], ['/audit', 'Audit'], ['/access', 'My grants'],
    ['/access/active', 'Active grants'], ['/admin/targets', 'Targets']]
  for (const [path, h1] of routes) {
    await page.goto(path)
    await expect(page.getByRole('heading', { level: 1, name: h1, exact: true })).toBeVisible()
    await expect(page.getByText('Loading')).toHaveCount(0)
    await axe(page)
  }
  await page.goto('/n/shop/s/web')
  await expect(page).toHaveTitle(/\[PROD\] \[ROLLOUT\]/) // UI-012
  await expect(page.locator('tbody .badge, .badge.large').first()).toBeVisible()
})

test('stack page reflects a scale event without reload; <script> name is text (UI-003)', async ({ page }) => {
  let alerted = false
  page.on('dialog', (d) => { alerted = true; d.dismiss() })
  await mockStack(page, stack(1, '<script>alert(1)</script>'))
  await page.route('**/api/v1/events*', sse('stack.updated', 'shop', stack(3, '<script>alert(1)</script>')))
  await page.goto('/n/shop/s/web')
  await expect(page.getByRole('cell', { name: '1/3' })).toBeVisible()
  await expect(page.getByRole('cell', { name: '3/3' })).toBeVisible()
  await expect(page.getByText('Deployment/<script>alert(1)</script>')).toBeVisible()
  expect(alerted).toBe(false)
})

test('deployment page goes from running to result live', async ({ page }) => {
  await mockStack(page, stack(3))
  await page.route('**/api/v1/events*', sse('deployment.updated', 'shop', dep('succeeded', { rollout: { state: 'done', result: 'healthy', workloads: [] } })))
  await page.goto('/deployments/d1')
  await expect(page.getByText('running', { exact: true })).toBeVisible()
  await expect(page.getByText('succeeded', { exact: true })).toBeVisible()
})

test('new audit event appears on top and is held while paused', async ({ page }) => {
  const csrf = (await (await page.request.get('/api/v1/me')).json()).csrf_token
  const mk = (slug: string) => page.request.post('/api/v1/namespaces', { headers: { 'X-CSRF-Token': csrf }, data: { slug, display_name: slug, environment_tier: 'non-prod' } })
  await page.goto('/audit')
  await expect(page.locator('.conn')).toHaveText(/Live/)
  await page.getByLabel('action').fill('namespace.create')
  await expect(page.getByRole('rowheader').first()).toHaveText('namespace.create') // shop from the axe test
  const rows = await page.getByRole('rowheader').count()
  await mk('live-a')
  await expect(page.getByRole('rowheader')).toHaveCount(rows + 1)
  await expect(page.getByRole('rowheader').first()).toHaveText('namespace.create')
  await page.getByRole('button', { name: 'Pause' }).click()
  await mk('live-b')
  await expect(page.getByText('1 new event')).toBeVisible()
  expect(await page.getByRole('rowheader').count()).toBe(rows + 1)
  await page.getByRole('button', { name: 'Resume' }).click()
  await expect(page.getByRole('rowheader')).toHaveCount(rows + 2)
})
