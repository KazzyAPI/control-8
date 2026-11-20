import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { setCsrf } from '../api/client'
import { applyEvent } from '../events/dispatch'
import { ToastProvider } from '../ui'
import { ApproverQueue } from './Access'
import { CreateAsset, DeployDialog, NewRevision, StackActions } from './Write'

// openapi-fetch captures globalThis.fetch when the client module loads, so install the stub first.
const fetchMock = vi.hoisted(() => { const m = vi.fn(); globalThis.fetch = ((...a: unknown[]) => m(...a)) as typeof fetch; return m })
const reply = (status: number, body: unknown, type = 'application/json') => new Response(JSON.stringify(body), { status, headers: { 'content-type': type } })

afterEach(() => { cleanup(); fetchMock.mockReset(); sessionStorage.clear() })
beforeEach(() => setCsrf('tok'))

const me = { id: 'u1', display_name: 'Ann', auth_method: 'oidc', global_admin: false, approver_for: [], active_grants: [] }
const stack = (over: object = {}) => ({ id: 's1', namespace: 'shop', slug: 'web', asset: 'web', target: 'prod-eu', runtime: 'kubernetes', environment_tier: 'prod',
  state: 'active', safety_profile: 'managed_rollout', services: [{ name: 'Deployment/api', kind: 'Deployment', containers: [{ name: 'api', image: 'r/api:1' }] }], ...over })
const grant = (over: object = {}) => ({ id: 'agr_1', requester: 'u1', namespace: 'shop', target: 'prod-eu', status: 'requested', duration_minutes: 60, reason: 'release', ...over })

function mount(el: React.ReactNode, who: object = me, seed: [unknown[], unknown][] = [[['grants', 'all'], { items: [] }]]) {
  const qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } })
  qc.setQueryData(['me'], who)
  for (const [k, v] of seed) qc.setQueryData(k, v)
  render(<QueryClientProvider client={qc}><ToastProvider><MemoryRouter><Routes><Route path="*" element={el} /></Routes></MemoryRouter></ToastProvider></QueryClientProvider>)
  return qc
}
const sent = () => fetchMock.mock.calls.map((c) => c[0] as Request)

it('UI-013/014: prod deploy needs the typed slug; as-is needs the checkbox; body carries the confirms and CSRF (UI-002)', async () => {
  fetchMock.mockResolvedValue(reply(202, { id: 'dep_1' }))
  mount(<DeployDialog s={stack({ safety_profile: 'managed_rollout' }) as any} r={{ number: 3, safety_profile: 'as_is' } as any} onClose={() => {}} />)
  expect(screen.getByText(/Safety profile changes: Managed rollout → As-is/)).toBeTruthy()
  const btn = screen.getByRole('button', { name: 'Deploy rev 3 to PROD — As-is' })
  fireEvent.click(btn)
  expect((await screen.findByRole('alert')).textContent).toMatch(/Choose the safety profile/)
  fireEvent.click(screen.getByLabelText('As-is'))
  fireEvent.click(btn)
  await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/Type "shop"/))
  fireEvent.change(screen.getByLabelText(/Type/), { target: { value: 'shop' } })
  fireEvent.click(btn)
  await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/no automatic rollback/))
  expect(fetchMock).not.toHaveBeenCalled()
  fireEvent.click(screen.getByLabelText(/I understand there is no automatic rollback/))
  fireEvent.click(btn)
  await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce())
  const req = sent()[0]
  expect(req.method).toBe('POST'); expect(req.url).toContain('/namespaces/shop/stacks/web/deployments')
  expect(req.headers.get('X-CSRF-Token')).toBe('tok')
  expect(await req.json()).toEqual({ revision: 3, confirm_safety_profile: 'as_is', confirm_environment_tier: 'prod' })
})

it('UI-016: prod buttons unlock when a grant becomes active and lock again at expiry, without reload', async () => {
  const qc = mount(<StackActions s={stack() as any} />)
  const teardown = screen.getByRole('button', { name: 'Teardown' }) as HTMLButtonElement
  const img = screen.getByRole('button', { name: /Update image of Deployment\/api api/ }) as HTMLButtonElement
  expect(teardown.disabled && img.disabled).toBe(true)
  await act(async () => applyEvent(qc, { type: 'access_grant.updated', at: '', namespace: 'shop', data: grant({ status: 'active', expires_at: new Date(Date.now() + 1500).toISOString() }) }))
  await waitFor(() => expect(teardown.disabled).toBe(false))
  expect(img.disabled).toBe(false)
  await waitFor(() => expect(teardown.disabled).toBe(true), { timeout: 4000 }) // expiry flips it back
})

it('UI-016/018: break-glass is never gated; non-prod is never gated', () => {
  mount(<StackActions s={stack() as any} />, { ...me, auth_method: 'break_glass' })
  expect((screen.getByRole('button', { name: 'Teardown' }) as HTMLButtonElement).disabled).toBe(false)
  cleanup()
  mount(<StackActions s={stack({ environment_tier: 'non-prod' }) as any} />)
  expect((screen.getByRole('button', { name: 'Teardown' }) as HTMLButtonElement).disabled).toBe(false)
})

it('API-004/UI error toasts: a 403 problem+json (missing CSRF) shows as an alert toast', async () => {
  setCsrf(null)
  fetchMock.mockResolvedValue(reply(403, { type: 'x', title: 'Forbidden', status: 403, detail: 'missing or invalid X-CSRF-Token', code: 'csrf_failed' }, 'application/problem+json'))
  mount(<CreateAsset ns="shop" />)
  fireEvent.change(screen.getByLabelText('Slug'), { target: { value: 'web' } })
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Web' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create asset' }))
  const t = await screen.findByText(/csrf_failed/)
  expect(t.getAttribute('role')).toBe('alert')
  expect(sent()[0].headers.get('X-CSRF-Token')).toBeNull()
})

it('06: the new revision draft survives a remount via sessionStorage and is posted with the violations shown inline', async () => {
  const el = <Routes><Route path="/n/:ns/s/:stack/revisions/new" element={<NewRevision />} /></Routes>
  const render1 = () => { const qc = new QueryClient(); return render(<QueryClientProvider client={qc}><ToastProvider><MemoryRouter initialEntries={['/n/shop/s/web/revisions/new']}>{el}</MemoryRouter></ToastProvider></QueryClientProvider>) }
  const r = render1()
  fireEvent.change(screen.getByLabelText(/Manifests/), { target: { value: 'kind: Pod' } })
  fireEvent.change(screen.getByLabelText('Safety profile'), { target: { value: 'as_is' } })
  r.unmount()
  render1()
  expect((screen.getByLabelText(/Manifests/) as HTMLTextAreaElement).value).toBe('kind: Pod')
  fetchMock.mockResolvedValue(reply(422, { type: 'x', title: 'Policy', status: 422, code: 'invalid_manifests', violations: [{ rule: 'registry_not_allowed', message: 'nope' }] }, 'application/problem+json'))
  fireEvent.click(screen.getByRole('button', { name: 'Create revision' }))
  expect(await screen.findByText(/registry_not_allowed: nope/)).toBeTruthy()
  expect(sessionStorage.getItem('c8:draft:shop/web')).toContain('kind: Pod') // kept on failure
})

it('GRT-020/025: approver queue shows requests live, hides own request decisions, removes decided rows', async () => {
  const qc = mount(<ApproverQueue />, { ...me, approver_for: ['shop'] }, [[['grants', 'all'], { items: [grant({ id: 'agr_own', requester: 'u1' })] }]])
  expect(screen.getByText('Your own request')).toBeTruthy()
  await act(async () => applyEvent(qc, { type: 'access_grant.updated', at: '', namespace: 'shop', data: grant({ id: 'agr_2', requester: 'u2' }) }))
  fetchMock.mockResolvedValue(reply(200, grant({ id: 'agr_2', status: 'active' })))
  fireEvent.change(await screen.findByLabelText('Minutes for agr_2'), { target: { value: '90' } })
  fireEvent.click(screen.getByRole('button', { name: 'Approve agr_2' }))
  expect(await screen.findByRole('alert')).toBeTruthy() // 90 > 60
  fireEvent.change(screen.getByLabelText('Minutes for agr_2'), { target: { value: '30' } })
  fireEvent.click(screen.getByRole('button', { name: 'Approve agr_2' }))
  await waitFor(() => expect(sent().some((r) => r.method === 'POST')).toBe(true))
  expect(await sent().find((r) => r.method === 'POST')!.json()).toEqual({ duration_minutes: 30 })
  await act(async () => applyEvent(qc, { type: 'access_grant.updated', at: '', namespace: 'shop', data: grant({ id: 'agr_2', requester: 'u2', status: 'active' }) }))
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Approve agr_2' })).toBeNull())
})
