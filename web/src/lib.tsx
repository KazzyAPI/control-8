import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { api, setCsrf } from './api/client'
import type { components } from './api/schema'
import { Notice } from './components/ui/Notice'
import { Skeleton, type Fallback } from './components/ui/Skeletons'
export { useFlash } from './components/ui/useFlash'

export type S = components['schemas']

export async function get<T>(path: string, params?: object): Promise<T> {
  const { data, response } = await (api as any).GET(path, { params })
  if (!data) throw Object.assign(new Error(`GET ${path} ${response.status}`), { status: response.status })
  return data as T
}
export const useGet = <T,>(key: unknown[], path: string, params?: object) =>
  useQuery({ queryKey: key, queryFn: () => get<T>(path, params) })

export async function fetchMe() {
  const { data, response } = await api.GET('/me')
  if (response.status === 401) { window.location.assign('/login'); return new Promise<never>(() => {}) } // UI-002: login is a full-page nav
  if (!data) throw new Error(`GET /me ${response.status}`)
  setCsrf(data.csrf_token ?? null)
  return data
}
export const useMe = () => useQuery({ queryKey: ['me'], queryFn: fetchMe })

// Loading / error / data render. Everything inside is rendered as React text (UI-003). `fallback` picks the skeleton shape.
export function Q<T>({ q, children, fallback = 'text' }: { q: UseQueryResult<T>; children: (d: T) => ReactNode; fallback?: Fallback }) {
  if (q.data !== undefined) return <>{children(q.data)}</>
  if (q.error) return <Notice tone="error" role="alert">{(q.error as any).status === 404 ? 'Not found.' : `Could not load: ${String(q.error)}`}</Notice>
  return <Skeleton kind={fallback} />
}

export function useTitle(t: string) { useEffect(() => { document.title = `${t} — Control 8` }, [t]) }

export const useNow = (ms = 1000) => {
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), ms); return () => clearInterval(t) }, [ms])
  return now
}
export const hhmm = (iso?: string | null) => (iso ? new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : '—')
export const when = (iso?: string | null) => (iso ? new Date(iso).toLocaleString([], { hour12: false }) : '—')

// UI-016 countdown text; null once expired.
export function remaining(expires: string | null | undefined, now: number) {
  if (!expires) return null
  const s = Math.floor((Date.parse(expires) - now) / 1000)
  if (s <= 0) return null
  return s >= 60 ? `${Math.ceil(s / 60)} min left` : `${s} s left`
}

export const sumReady = (s: S['Stack']) => (s.services ?? []).reduce((a, v) => [a[0] + (v.ready_replicas ?? 0), a[1] + (v.desired_replicas ?? 0)], [0, 0])
export const profileTag = (p?: string | null) => (p === 'managed_rollout' ? '[ROLLOUT]' : p === 'as_is' ? '[AS-IS]' : p === 'parachute' ? '[PARACHUTE]' : '')

// UI-016/018: live prod-access state for (namespace, target). Re-evaluates every second, so expiry flips it without reload.
export function useProdAccess(ns: string, target: string, tier?: string) {
  const me = useMe().data!
  const now = useNow()
  const grants = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  const mine = (grants.data?.items ?? []).filter((g) => g.requester === me.id && g.namespace === ns && g.target === target)
  const active = mine.find((g) => g.status === 'active' && remaining(g.expires_at, now))
  const state = tier !== 'prod' ? 'open' as const : me.auth_method === 'break_glass' ? 'bypass' as const : active ? 'active' as const
    : mine.some((g) => g.status === 'requested') ? 'pending' as const : 'none' as const
  return { state, active, now, allowed: state === 'open' || state === 'bypass' || state === 'active' }
}
