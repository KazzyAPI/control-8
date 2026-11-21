import { Link } from 'react-router'
import { get, sumReady, useFlash, hhmm, type S } from '../lib'
import { ProfileBadge, StatusBadge, TierBadge } from './Badges'
import { RowMenu } from './Parts'
import { useQuery } from '@tanstack/react-query'

// UI-023: shown on every stack of an unreachable target.
export function TargetNote({ target }: { target?: string }) {
  const q = useQuery({ queryKey: ['targets'], queryFn: () => get<{ items: S['Target'][] }>('/targets') })
  const t = q.data?.items.find((x) => x.name === target)
  if (!t || t.reachable !== false) return null
  return <div className="alert alert-warning my-2" role="status"><span>Cluster {t.name} unreachable {'—'} status as of {hhmm(t.last_seen_at)}</span></div>
}

// UI-011/015/024: one stack row, used by overview, namespace and asset screens.
export function StackRow({ s }: { s: S['Stack'] }) {
  const [r, d] = sumReady(s)
  const n = useFlash(JSON.stringify([s.health, s.safety_profile, r, d, s.state]))
  const href = `/n/${s.namespace}/s/${s.slug}`
  return (
    <tr key={n} className={n ? 'flash' : ''}>
      <th scope="row"><Link to={href}>{s.slug}</Link> <TierBadge tier={s.environment_tier} /></th>
      <td><ProfileBadge profile={s.safety_profile} /></td>
      <td><StatusBadge text={s.state === 'active' ? (s.health ?? 'unknown') : s.state} /></td>
      <td>{r}/{d} <progress className={`progress w-16 align-middle ${r < d ? 'progress-warning' : 'progress-success'}`} value={r} max={Math.max(d, 1)} aria-hidden="true" /></td>
      <td>{s.target}</td>
      <td><RowMenu label={`Actions for ${s.slug}`}>
        <li><Link to={href}>Open stack</Link></li>
        <li><Link to={`${href}/revisions/new`}>New revision</Link></li></RowMenu></td>
    </tr>
  )
}
export const STACK_HEAD = ['Stack', 'Safety profile', 'Health', 'Ready', 'Target', 'Actions']
