import { Link } from 'react-router'
import { get, sumReady, useFlash, hhmm, type S } from '../lib'
import { ProfileBadge, TierBadge } from './Badges'
import { useQuery } from '@tanstack/react-query'

// UI-023: shown on every stack of an unreachable target.
export function TargetNote({ target }: { target?: string }) {
  const q = useQuery({ queryKey: ['targets'], queryFn: () => get<{ items: S['Target'][] }>('/targets') })
  const t = q.data?.items.find((x) => x.name === target)
  if (!t || t.reachable !== false) return null
  return <p className="warn" role="status">Cluster {t.name} unreachable {'—'} status as of {hhmm(t.last_seen_at)}</p>
}

// UI-011/015/024: one stack row, used by overview, namespace and asset screens.
export function StackRow({ s }: { s: S['Stack'] }) {
  const [r, d] = sumReady(s)
  const n = useFlash(JSON.stringify([s.health, s.safety_profile, r, d, s.state]))
  return (
    <tr key={n} className={n ? 'flash' : ''}>
      <th scope="row"><Link to={`/n/${s.namespace}/s/${s.slug}`}>{s.slug}</Link> <TierBadge tier={s.environment_tier} /></th>
      <td><ProfileBadge profile={s.safety_profile} /></td>
      <td>{s.state === 'active' ? (s.health ?? 'unknown') : s.state}</td>
      <td>{r}/{d}</td>
      <td>{s.target}</td>
    </tr>
  )
}
export const STACK_HEAD = ['Stack', 'Safety profile', 'Health', 'Ready', 'Target']
