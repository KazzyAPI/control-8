import type { QueryClient } from '@tanstack/react-query'
import type { components } from '../api/schema'

export type EventType = components['schemas']['EventType']
export const EVENT_TYPES: EventType[] = ['stack.updated', 'stack.removed', 'revision.created', 'deployment.updated',
  'access_grant.updated', 'audit.created', 'target.status', 'reset', 'session.ended']
export type C8Event = { type: EventType; at: string; namespace?: string | null; data: any }

// Query keys: ['inventory'] {items}, ['stack',ns,slug], ['revisions',ns], ['deployment',id],
// ['deployments',ns,stack] {items}, ['grants'] {items}, ['audit'] {items}, ['targets'] {items}, ['target',name].
type Keyed = { id?: string | number; name?: string }
const same = (a: Keyed, b: Keyed) => (a.id !== undefined ? a.id === b.id : a.name === b.name)

// Patch an item into every cached list under `key`; unknown shapes are left alone. `front` prepends new items (audit feed).
function upsert(qc: QueryClient, key: unknown[], item: Keyed, front = false) {
  qc.setQueriesData<{ items: Keyed[] }>({ queryKey: key }, (old) => {
    if (!old || !Array.isArray(old.items)) return old
    const i = old.items.findIndex((x) => same(x, item))
    if (i >= 0) return { ...old, items: old.items.map((x, j) => (j === i ? item : x)) }
    return { ...old, items: front ? [item, ...old.items] : [...old.items, item] }
  })
}

// UI-020: one cache update per event type; reset invalidates everything.
export function applyEvent(qc: QueryClient, ev: C8Event) {
  const d = ev.data
  switch (ev.type) {
    case 'stack.updated':
      qc.setQueryData(['stack', d.namespace, d.slug], d)
      upsert(qc, ['inventory'], d)
      break
    case 'stack.removed':
      qc.removeQueries({ queryKey: ['stack', d.namespace, d.slug] })
      qc.setQueriesData<{ items: any[] }>({ queryKey: ['inventory'] }, (old) =>
        old && { ...old, items: old.items.filter((s) => !(s.namespace === d.namespace && s.slug === d.slug)) })
      break
    case 'revision.created': // Revision carries no stack slug, so refetch the namespace's revision queries
      qc.invalidateQueries({ queryKey: ['revisions', ev.namespace] })
      break
    case 'deployment.updated':
      qc.setQueryData(['deployment', d.id], d)
      upsert(qc, ['deployments', d.namespace, d.stack], d)
      break
    case 'access_grant.updated':
      qc.setQueryData(['grant', d.id], d)
      upsert(qc, ['grants'], d)
      qc.invalidateQueries({ queryKey: ['me'] }) // active_grants / approver_for derive from grants
      break
    case 'audit.created':
      upsert(qc, ['audit'], d, true)
      break
    case 'target.status':
      qc.setQueryData(['target', d.name], d)
      upsert(qc, ['targets'], d)
      break
    case 'reset':
      qc.invalidateQueries()
      break
    case 'session.ended':
      break // handled by the stream hook (UI-025)
  }
}
