import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Browser, type Page } from '@playwright/test'

// M15 write flows. The e2e server has break-glass only (no OIDC, no cluster), so a second human approver cannot log in
// (D-97). Scenarios that need one use two browser contexts whose API and SSE traffic is answered at the network edge from
// one shared in-memory state; scenarios that only need the real API (CSRF, forms, axe) run against the Go binary.
const json = (body: unknown, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) })
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
const axe = async (page: Page) => {
  const { violations } = await new AxeBuilder({ page }).analyze()
  expect(violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')).toEqual([])
}

type World = { tier: 'prod' | 'non-prod'; profile: 'managed_rollout' | 'as_is'; grants: any[]; posts: { path: string; csrf: string | undefined; body: any }[] }
const world = (tier: World['tier'], profile: World['profile'] = 'managed_rollout'): World => ({ tier, profile, grants: [], posts: [] })

async function mockWorld(page: Page, w: World, who: { id: string; approver_for: string[] }) {
  const stack = { id: 's1', namespace: 'shop', slug: 'web', asset: 'web', target: 'prod-eu', runtime: 'kubernetes', environment_tier: w.tier, state: 'active',
    safety_profile: 'managed_rollout', health: 'healthy', desired_revision: 1,
    services: [{ kind: 'Deployment', name: 'api', desired_replicas: 1, ready_replicas: 1, image_ref: 'r/api:1', containers: [{ name: 'api', image: 'r/api:1' }] }] }
  const api = (p: string, h: (r: any) => any) => page.route(`**/api/v1${p}`, h)
  await api('/**', (r) => r.fulfill(json({ items: [] })))
  await api('/me', (r) => r.fulfill(json({ id: who.id, display_name: who.id, auth_method: 'oidc', global_admin: false, csrf_token: 'tok', approver_for: who.approver_for, active_grants: [] })))
  await api('/inventory', (r) => r.fulfill(json({ items: [stack] })))
  await api('/namespaces', (r) => r.fulfill(json({ items: [{ slug: 'shop', display_name: 'Shop', environment_tier: w.tier, approver_group: 'shop-approvers' }] })))
  await api('/targets', (r) => r.fulfill(json({ items: [{ name: 'prod-eu', kind: 'kubernetes', environment_tier: w.tier, reachable: true }] })))
  await api('/namespaces/shop/stacks/web', (r) => r.fulfill(json(stack)))
  await api('/namespaces/shop/stacks/web/revisions', (r) => r.fulfill(json({ items: [{ number: 1, safety_profile: w.profile }] })))
  await api('/namespaces/shop/stacks/web/revisions/1', (r) => r.fulfill(json({ number: 1, safety_profile: w.profile, origin: 'user', policy_result: { allowed: true }, inventory: [], manifests_input: 'kind: Deployment', created_by: 'ann' })))
  await api('/namespaces/shop/stacks/web/deployments', (r) => {
    if (r.request().method() === 'POST') { w.posts.push({ path: 'deployments', csrf: r.request().headers()['x-csrf-token'], body: r.request().postDataJSON() }); return r.fulfill(json({ id: 'd1', status: 'queued', action: 'apply', runtime: 'kubernetes', safety_profile: w.profile, environment_tier: w.tier }, 202)) }
    return r.fulfill(json({ items: [] }))
  })
  await api('/deployments/d1', (r) => r.fulfill(json({ id: 'd1', namespace: 'shop', stack: 'web', status: 'queued', action: 'apply', runtime: 'kubernetes', safety_profile: w.profile, environment_tier: w.tier })))
  await api('/access-grants', (r) => {
    if (r.request().method() === 'POST') {
      w.grants = [{ id: 'agr_1', requester: 'ann', ...r.request().postDataJSON(), status: 'requested', environment_tier: 'prod', approver_group: 'shop-approvers' }]
      return r.fulfill(json(w.grants[0], 201))
    }
    return r.fulfill(json({ items: w.grants }))
  })
  await api('/access-grants/agr_1/approve', (r) => {
    w.posts.push({ path: 'approve', csrf: r.request().headers()['x-csrf-token'], body: r.request().postDataJSON() })
    w.grants = [{ ...w.grants[0], status: 'active', approver: 'bob', expires_at: new Date(Date.now() + 12000).toISOString() }]
    return r.fulfill(json(w.grants[0]))
  })
}
// Delivers one access_grant.updated event once `pick` returns a grant; the EventSource then waits `retry` before reconnecting.
async function sseGrant(page: Page, pick: () => any) {
  await page.route('**/api/v1/events*', async (r) => {
    try {
      for (let i = 0; i < 150 && !pick(); i++) await sleep(100)
      const data = pick()
      const body = data ? `retry: 60000\nid: 1\nevent: access_grant.updated\ndata: ${JSON.stringify({ type: 'access_grant.updated', at: new Date().toISOString(), namespace: 'shop', data })}\n\n` : 'retry: 60000\n\n'
      await r.fulfill({ contentType: 'text/event-stream', body })
    } catch { /* page navigated away */ }
  })
}
const ctx = async (browser: Browser, baseURL: string | undefined) => { const c = await browser.newContext({ baseURL }); return { c, page: await c.newPage() } }

test('non-prod deploy happy path; as-is needs the checkbox; axe on the dialog (UI-013, UI-033)', async ({ browser, baseURL }) => {
  const { c, page } = await ctx(browser, baseURL)
  const w = world('non-prod', 'as_is')
  await mockWorld(page, w, { id: 'ann', approver_for: [] })
  await page.goto('/n/shop/s/web/revisions/1')
  await page.getByRole('button', { name: 'Deploy revision 1' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await axe(page)
  const submit = page.getByRole('button', { name: 'Deploy rev 1 to NON-PROD — As-is' })
  await page.getByLabel('As-is', { exact: true }).check()
  await submit.click()
  await expect(page.getByRole('alert')).toContainText('no automatic rollback')
  expect(w.posts).toHaveLength(0)
  await page.getByLabel(/I understand there is no automatic rollback/).check()
  await submit.click()
  await expect(page.getByRole('heading', { name: 'Deployment d1' })).toBeVisible()
  expect(w.posts[0]).toEqual({ path: 'deployments', csrf: 'tok', body: { revision: 1, confirm_safety_profile: 'as_is', confirm_environment_tier: 'non-prod' } })
  await c.close()
})

test('prod: blocked, request access, approver sees it live, approves, requester unlocks live, deploys, expiry locks again (UI-016, GRT-020)', async ({ browser, baseURL }) => {
  test.setTimeout(90000)
  const w = world('prod')
  const req = await ctx(browser, baseURL)
  const apr = await ctx(browser, baseURL)
  await mockWorld(req.page, w, { id: 'ann', approver_for: [] })
  await mockWorld(apr.page, w, { id: 'bob', approver_for: ['shop'] })
  await sseGrant(req.page, () => w.grants.find((g) => g.status === 'active'))
  await sseGrant(apr.page, () => w.grants.find((g) => g.status === 'requested'))

  await apr.page.goto('/access/approvals')
  await expect(apr.page.getByText('No requests waiting.')).toBeVisible()

  const p = req.page
  await p.goto('/n/shop/s/web/revisions/1')
  const deploy = p.getByRole('button', { name: 'Deploy revision 1' })
  await expect(deploy).toBeDisabled()
  await p.getByRole('link', { name: 'Request prod access' }).click()
  await p.getByLabel('Reason').fill('short')
  await p.getByRole('button', { name: 'Request access' }).click()
  await expect(p.getByRole('alert')).toContainText('10 to 500')
  await axe(p)
  await p.getByLabel('Reason').fill('Release 1.4 of shop')
  await p.getByRole('button', { name: 'Request access' }).click()
  await expect(p.getByRole('heading', { name: 'My grants' })).toBeVisible()
  await p.goBack(); await p.goBack()
  await expect(p.getByText('Awaiting approval')).toBeVisible()

  // approver sees the request appear without reload, then approves a shorter grant
  await expect(apr.page.getByRole('button', { name: 'Approve agr_1' })).toBeVisible({ timeout: 15000 })
  await axe(apr.page)
  await apr.page.getByRole('button', { name: 'Approve agr_1' }).click()
  await expect(apr.page.getByText('No requests waiting.')).toBeVisible()
  expect(w.posts.find((x) => x.path === 'approve')).toMatchObject({ csrf: 'tok', body: { duration_minutes: 60 } })

  // requester's buttons unlock live
  await expect(p.getByText(/Access until/)).toBeVisible({ timeout: 15000 })
  await expect(deploy).toBeEnabled()
  await deploy.click()
  await p.getByLabel('Managed rollout', { exact: true }).check()
  await p.getByRole('button', { name: 'Deploy rev 1 to PROD — Managed rollout' }).click()
  await expect(p.getByRole('alert')).toContainText('Type "shop"')
  await p.getByLabel(/to confirm this production deploy/).fill('shop')
  await p.getByRole('button', { name: 'Deploy rev 1 to PROD — Managed rollout' }).click()
  await expect(p.getByRole('heading', { name: 'Deployment d1' })).toBeVisible()
  expect(w.posts.find((x) => x.path === 'deployments')!.body).toEqual({ revision: 1, confirm_safety_profile: 'managed_rollout', confirm_environment_tier: 'prod' })

  // expiry flips the button back without reload
  await p.goBack()
  await expect(deploy).toBeDisabled({ timeout: 20000 })
  await expect(p.getByRole('link', { name: 'Request prod access' })).toBeVisible()
  await req.c.close(); await apr.c.close()
})

// Real binary below: break-glass (global admin), real API.
test.describe('real API', () => {
  test.beforeEach(async ({ page }) => {
    expect((await page.request.post('/auth/break-glass', { data: { password: 'e2e-password' } })).status()).toBe(204)
    const csrf = (await (await page.request.get('/api/v1/me')).json()).csrf_token
    await page.request.post('/api/v1/namespaces', { headers: { 'X-CSRF-Token': csrf }, data: { slug: 'wshop', display_name: 'WShop', environment_tier: 'non-prod' } })
  })

  test('create asset live, set approver group, and a missing CSRF token is a 403 error toast', async ({ page }) => {
    await page.goto('/n/wshop')
    await expect(page.getByRole('heading', { name: 'WShop' })).toBeVisible()
    await page.getByRole('tab', { name: 'Settings' }).click()
    await page.getByLabel('Slug').first().fill('Bad Slug')
    await page.getByRole('button', { name: 'Create asset' }).click()
    await expect(page.getByRole('alert').first()).toContainText('lowercase')
    await page.getByLabel('Slug').first().fill('webapp')
    await page.getByLabel('Name', { exact: true }).fill('Web app')
    await page.getByRole('button', { name: 'Create asset' }).click()
    await page.getByRole('tab', { name: 'Overview' }).click()
    await expect(page.getByRole('link', { name: 'Web app' })).toBeVisible()
    await page.getByRole('tab', { name: 'Settings' }).click()
    await page.getByRole('textbox', { name: 'Approver group' }).fill('wshop-approvers')
    await page.getByRole('button', { name: 'Save approver group' }).click()
    await expect(page.getByText('Approver group: wshop-approvers')).toBeVisible()
    await axe(page)

    await page.route('**/api/v1/namespaces/wshop/assets', (r) => {
      const h = { ...r.request().headers() }; delete h['x-csrf-token']
      return r.continue({ headers: h })
    })
    await page.getByLabel('Slug').first().fill('second')
    await page.getByLabel('Name', { exact: true }).fill('Second')
    await page.getByRole('button', { name: 'Create asset' }).click()
    await expect(page.getByRole('alert').filter({ hasText: 'csrf_failed' })).toBeVisible()
  })

  test('axe passes on every new write screen', async ({ page }) => {
    for (const [path, h1] of [['/n/wshop/s/none/revisions/new', 'New revision'], ['/access/request', 'Request access'], ['/access/approvals', 'Approver queue'],
      ['/approvals', 'Approvals'], ['/admin/access', 'Access administration'], ['/admin/policy', 'Global policy']]) {
      await page.goto(path)
      await expect(page.getByRole('heading', { level: 1, name: h1, exact: true })).toBeVisible()
      await expect(page.getByText('Loading')).toHaveCount(0)
      await axe(page)
    }
  })

  test('admin access: add and remove a role binding; policy saves', async ({ page }) => {
    await page.goto('/admin/access')
    await page.getByLabel('IdP group').fill('wshop-devs')
    await page.getByRole('combobox', { name: /^Namespace/ }).selectOption('wshop')
    await page.getByRole('combobox', { name: /^Role/ }).selectOption('operator')
    await page.getByRole('button', { name: 'Add binding' }).click()
    await expect(page.getByRole('rowheader', { name: 'wshop-devs' })).toBeVisible()
    await page.getByRole('button', { name: 'Delete binding wshop-devs operator' }).click()
    await expect(page.getByRole('rowheader', { name: 'wshop-devs' })).toHaveCount(0)
    await page.goto('/admin/policy')
    await page.getByLabel(/allowed registries/).fill('ghcr.io/acme/')
    await page.getByRole('button', { name: 'Save policy' }).click()
    await expect(page.getByText('Policy saved')).toBeVisible()
  })
})
