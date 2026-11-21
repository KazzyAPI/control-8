import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { get, hhmm, sumReady, type S } from '../lib'
import { DataTable, EmptyState, Icon, Notice, ProfileBadge, StatusBadge, TierBadge, type Column } from './ui'

// UI-023: shown on every stack of an unreachable target.
export function TargetNote({ target }: { target?: string }) {
  const q = useQuery({ queryKey: ['targets'], queryFn: () => get<{ items: S['Target'][] }>('/targets') })
  const t = q.data?.items.find((x) => x.name === target)
  if (!t || t.reachable !== false) return null
  return <Notice tone="warning" title={`Cluster ${t.name} is unreachable`}>Status as of {hhmm(t.last_seen_at)}; changes cannot be applied until it is back.</Notice>
}

export const healthOf = (s: S['Stack']) => (s.state === 'active' ? (s.health ?? 'unknown') : s.state)
export const ReadyBar = ({ r, d }: { r: number; d: number }) => <>{r}/{d} <progress className={`progress w-16 align-middle ${r < d ? 'progress-warning' : 'progress-success'}`} value={r} max={Math.max(d, 1)} aria-hidden="true" /></>

// UI-011/015/024: stack columns shared by the overview, namespace and asset tables (the row flashes when it changes live).
const COLUMNS: Column<S['Stack']>[] = [
  { key: 'stack', header: 'Stack', rowHeader: true, cell: (s) => <span className="flex flex-wrap items-center gap-2"><Link to={`/n/${s.namespace}/s/${s.slug}`} className="font-semibold">{s.slug}</Link> <TierBadge tier={s.environment_tier} /></span> },
  { key: 'profile', header: 'Safety profile', hideBelow: 'md', cell: (s) => <ProfileBadge profile={s.safety_profile} /> },
  { key: 'health', header: 'Health', cell: (s) => <StatusBadge text={healthOf(s)} /> },
  { key: 'ready', header: 'Ready', cell: (s) => { const [r, d] = sumReady(s); return <ReadyBar r={r} d={d} /> } },
  { key: 'target', header: 'Target', hideBelow: 'sm', cell: (s) => <span className="inline-flex items-center gap-1"><Icon name="server" className="size-4 text-base-content/60" />{s.target}</span> },
]
const sig = (s: S['Stack']) => JSON.stringify([s.health, s.safety_profile, sumReady(s), s.state])

export function StackTable({ stacks, label = 'Stacks', loading, empty }: { stacks: S['Stack'][]; label?: string; loading?: boolean; empty?: ReactNode }) {
  return <DataTable label={label} columns={COLUMNS} rows={stacks} rowKey={(s) => s.id} rowSig={sig} loading={loading} empty={empty} actionsLabel={(s) => `Actions for ${s.slug}`}
    rowActions={(s) => <>
      <li><Link to={`/n/${s.namespace}/s/${s.slug}`}><Icon name="layers" className="size-4" />Open stack</Link></li>
      <li><Link to={`/n/${s.namespace}/s/${s.slug}/revisions/new`}><Icon name="plus" className="size-4" />New revision</Link></li></>} />
}
export const NoStacks = ({ to, cta }: { to?: string; cta?: string }) => <EmptyState icon="layers" to={to} cta={cta} hint="A stack is a set of manifests deployed to one target.">No stacks.</EmptyState>
