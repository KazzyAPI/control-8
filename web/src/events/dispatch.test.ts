import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { applyEvent, type C8Event } from './dispatch'

const ev = (type: C8Event['type'], data: any, namespace: string | null = 'shop'): C8Event => ({ type, at: '2026-01-01T00:00:00Z', namespace, data })
const stack = { id: 's1', namespace: 'shop', slug: 'web', state: 'active' }

describe('UI-020 event -> cache dispatcher', () => {
  it('stack.updated sets detail and upserts inventory', () => {
    const qc = new QueryClient()
    qc.setQueryData(['inventory'], { items: [{ ...stack, state: 'draft' }, { id: 's2', namespace: 'shop', slug: 'db' }] })
    applyEvent(qc, ev('stack.updated', stack))
    expect(qc.getQueryData(['stack', 'shop', 'web'])).toEqual(stack)
    expect((qc.getQueryData(['inventory']) as any).items[0].state).toBe('active')
    applyEvent(qc, ev('stack.updated', { id: 's3', namespace: 'shop', slug: 'new' }))
    expect((qc.getQueryData(['inventory']) as any).items).toHaveLength(3)
  })
  it('stack.removed drops detail and list row', () => {
    const qc = new QueryClient()
    qc.setQueryData(['stack', 'shop', 'web'], stack)
    qc.setQueryData(['inventory'], { items: [stack] })
    applyEvent(qc, ev('stack.removed', { namespace: 'shop', slug: 'web' }))
    expect(qc.getQueryData(['stack', 'shop', 'web'])).toBeUndefined()
    expect((qc.getQueryData(['inventory']) as any).items).toEqual([])
  })
  it('revision.created invalidates the namespace revisions', () => {
    const qc = new QueryClient()
    qc.setQueryData(['revisions', 'shop', 'web'], { items: [] })
    applyEvent(qc, ev('revision.created', { id: 'r1', number: 2 }))
    expect(qc.getQueryState(['revisions', 'shop', 'web'])?.isInvalidated).toBe(true)
  })
  it('deployment.updated sets detail and upserts the stack list', () => {
    const qc = new QueryClient()
    const d = { id: 'd1', namespace: 'shop', stack: 'web', status: 'running' }
    qc.setQueryData(['deployments', 'shop', 'web'], { items: [{ ...d, status: 'queued' }] })
    applyEvent(qc, ev('deployment.updated', d))
    expect(qc.getQueryData(['deployment', 'd1'])).toEqual(d)
    expect((qc.getQueryData(['deployments', 'shop', 'web']) as any).items[0].status).toBe('running')
  })
  it('access_grant.updated upserts grants and refreshes /me', () => {
    const qc = new QueryClient()
    qc.setQueryData(['grants'], { items: [] })
    qc.setQueryData(['me'], {})
    applyEvent(qc, ev('access_grant.updated', { id: 'g1', status: 'approved' }))
    expect((qc.getQueryData(['grants']) as any).items).toHaveLength(1)
    expect(qc.getQueryState(['me'])?.isInvalidated).toBe(true)
  })
  it('audit.created prepends to the feed', () => {
    const qc = new QueryClient()
    qc.setQueryData(['audit'], { items: [{ id: 1 }] })
    applyEvent(qc, ev('audit.created', { id: 2 }))
    expect((qc.getQueryData(['audit']) as any).items.map((x: any) => x.id)).toEqual([2, 1])
  })
  it('target.status sets detail and upserts by name', () => {
    const qc = new QueryClient()
    qc.setQueryData(['targets'], { items: [{ name: 'k', reachable: false }] })
    applyEvent(qc, ev('target.status', { name: 'k', reachable: true }, null))
    expect((qc.getQueryData(['targets']) as any).items).toEqual([{ name: 'k', reachable: true }])
    expect(qc.getQueryData(['target', 'k'])).toEqual({ name: 'k', reachable: true })
  })
  it('reset invalidates every query', () => {
    const qc = new QueryClient()
    qc.setQueryData(['a'], 1)
    qc.setQueryData(['b', 2], 2)
    applyEvent(qc, ev('reset', { reason: 'buffer' }, null))
    expect(qc.getQueryState(['a'])?.isInvalidated && qc.getQueryState(['b', 2])?.isInvalidated).toBe(true)
  })
  it('session.ended leaves the cache alone', () => {
    const qc = new QueryClient()
    qc.setQueryData(['a'], 1)
    applyEvent(qc, ev('session.ended', { reason: 'expired' }, null))
    expect(qc.getQueryState(['a'])?.isInvalidated).toBe(false)
  })
})
