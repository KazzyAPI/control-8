import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, expect, it } from 'vitest'
import { applyEvent } from '../events/dispatch'
import { Audit, Grants, Targets } from './Feeds'
import { Overview } from './Browse'
import { Deployment, Stack } from './Stack'

afterEach(cleanup)
const soon = () => new Date(Date.now() + 52 * 60000).toISOString()
const me = { id: 'u1', display_name: 'Ann', auth_method: 'oidc', global_admin: true, approver_for: [], active_grants: [] }
const stack = (over: object = {}) => ({ id: 's1', namespace: 'shop', slug: 'web', asset: 'web', target: 'prod-eu', runtime: 'kubernetes', environment_tier: 'prod',
  state: 'active', safety_profile: 'managed_rollout', health: 'healthy',
  services: [{ kind: 'Deployment', name: 'api', desired_replicas: 3, ready_replicas: 3, image_ref: 'r/api:1', image_id: 'sha256:abc' }], ...over })

function mount(path: string, el: React.ReactNode, seed: [unknown[], unknown][]) {
  const qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } })
  qc.setQueryData(['me'], me)
  for (const [k, v] of seed) qc.setQueryData(k, v)
  render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[path]}><Routes><Route path="*" element={el} /></Routes></MemoryRouter></QueryClientProvider>)
  return qc
}
const fullSeed: [unknown[], unknown][] = [[['grants', 'all'], { items: [] }], [['targets'], { items: [] }]]

it('UI-010/011/012/003: stack page badges, title suffixes, script text, live scale (M14)', async () => {
  const evil = '<script>alert(1)</script>'
  const qc = mount('/n/shop/s/web', <Routes><Route path="/n/:ns/s/:stack" element={<Stack />} /></Routes>, [
    ...fullSeed, [['stack', 'shop', 'web'], stack({ services: [{ kind: 'Deployment', name: evil, desired_replicas: 3, ready_replicas: 3 }] })],
    [['deployments', 'shop', 'web'], { items: [] }], [['revisions', 'shop', 'web'], { items: [] }]])
  expect(screen.getByText(`Deployment/${evil}`)).toBeTruthy()
  expect(document.querySelector('script')).toBeNull()
  expect(document.title).toContain('[PROD] [ROLLOUT]')
  expect(screen.getAllByText(/Managed rollout/).length).toBeGreaterThan(0)
  expect(screen.getByText('3/3', { selector: 'td' })).toBeTruthy()
  await act(async () => applyEvent(qc, { type: 'stack.updated', at: '', namespace: 'shop', data: stack({ rollout_frozen: true, services: [{ kind: 'Deployment', name: evil, desired_replicas: 5, ready_replicas: 2 }] }) }))
  expect(await screen.findByText('2/5', { selector: 'td' })).toBeTruthy()
  expect(screen.getByRole('alert').textContent).toMatch(/Automatic updates stopped/) // UI-017
})

it('UI-011/015/032: overview rows carry text badges', async () => {
  mount('/', <Overview />, [...fullSeed, [['inventory'], { items: [stack(), stack({ id: 's2', slug: 'db', safety_profile: 'as_is', environment_tier: 'non-prod' })] }],
    [['namespaces'], { items: [{ slug: 'shop' }, { slug: 'empty' }] }]])
  for (const t of [/As-is/, /Managed rollout/, /^PROD$/, /non-prod/, /No stacks/]) expect(screen.getAllByText(t).length).toBeGreaterThan(0)
})

it('deployment page shows progress then result live', async () => {
  const dep = { id: 'd1', status: 'running', environment_tier: 'prod', safety_profile: 'as_is', grant_bypass: 'break_glass',
    rollout: { state: 'waiting', workloads: [{ kind: 'Deployment', name: 'api', desired: 3, updated: 1, ready: 1, available: 1 }] } }
  const qc = mount('/deployments/d1', <Routes><Route path="/deployments/:id" element={<Deployment />} /></Routes>, [[['deployment', 'd1'], dep], [['deployment-output', 'd1', 'running'], { output: 'applying' }]])
  expect(screen.getByText('running')).toBeTruthy()
  expect(screen.getByText(/grant bypassed/)).toBeTruthy() // UI-018
  await act(async () => applyEvent(qc, { type: 'deployment.updated', at: '', namespace: 'shop', data: { ...dep, status: 'succeeded', rollout: { ...dep.rollout, state: 'done', result: 'healthy' } } }))
  expect(await screen.findByText('succeeded', { selector: '.badge' })).toBeTruthy()
  expect(screen.getByText('Rollout result: healthy')).toBeTruthy()
})

it('audit feed: new event on top, held while paused', async () => {
  const ev = (id: number) => ({ id, at: '2026-01-01T00:00:00Z', action: `act${id}`, outcome: 'success' })
  const qc = mount('/audit', <Audit />, [[['audit', '', '', '', ''], { items: [ev(2), ev(1)] }]])
  await act(async () => applyEvent(qc, { type: 'audit.created', at: '', data: ev(3) }))
  await waitFor(() => expect(within(screen.getByRole("list", { name: "Audit events" })).getAllByRole("listitem")[0].textContent).toContain("act3"))
  fireEvent.click(screen.getByText('Pause'))
  await act(async () => applyEvent(qc, { type: 'audit.created', at: '', data: ev(4) }))
  expect(await screen.findByText('1 new event')).toBeTruthy()
  expect(within(screen.getByRole("list", { name: "Audit events" })).getAllByRole("listitem")[0].textContent).toContain("act3")
  fireEvent.click(screen.getByText('Resume'))
  await waitFor(() => expect(within(screen.getByRole("list", { name: "Audit events" })).getAllByRole("listitem")[0].textContent).toContain("act4"))
})

it('grants: countdown for active, expired rows leave the active list', async () => {
  const g = (id: string, expires_at: string) => ({ id, requester: 'u1', namespace: 'shop', target: 't', status: 'active', expires_at, reason: 'r' })
  mount('/access/active', <Grants active />, [[['grants', 'all'], { items: [g('a', soon()), g('b', new Date(Date.now() - 1000).toISOString())] }]])
  expect(screen.getByText(/min left/)).toBeTruthy()
  expect(screen.getAllByRole("timer").length).toBe(1) // only the live grant counts down
})

it('admin targets table (global_admin only)', async () => {
  mount('/admin/targets', <Targets />, [[['targets'], { items: [{ name: 'prod-eu', kind: 'kubernetes', environment_tier: 'prod', reachable: false, sync: 'down', last_error: 'dial tcp' }] }]])
  expect(screen.getByText('dial tcp')).toBeTruthy()
})
