import { useState } from 'react'
import { Q, remaining, Table, useGet, useMe, useNow, useTitle, when, hhmm, type S } from '../lib'
import { TierBadge } from '../components/Badges'

// UI audit: newest on top; pausing freezes the view at the top id seen, later events are only counted.
export function Audit() {
  useTitle('Audit')
  const [f, setF] = useState({ namespace: '', action: '', actor_id: '', grant_id: '' })
  const [pausedAt, setPausedAt] = useState<number | null>(null)
  const query = Object.fromEntries(Object.entries(f).filter(([, v]) => v))
  const q = useGet<{ items: S['AuditEvent'][] }>(['audit', f.namespace, f.action, f.actor_id, f.grant_id], '/audit', { query })
  // The live dispatcher prepends every event to every cached filter variant, so filter again here.
  const match = (e: S['AuditEvent']) => (!f.namespace || e.namespace === f.namespace) && (!f.action || e.action === f.action)
    && (!f.actor_id || e.actor_id === f.actor_id) && (!f.grant_id || e.target_id === f.grant_id)
  return (
    <>
      <h1>Audit</h1>
      <form className="filters" onSubmit={(e) => e.preventDefault()}>
        {(['namespace', 'action', 'actor_id', 'grant_id'] as const).map((k) =>
          <label key={k}>{k.replace('_', ' ')} <input value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} /></label>)}
      </form>
      <Q q={q}>{(d) => {
        const items = d.items.filter(match)
        const shown = pausedAt === null ? items : items.filter((e) => (e.id ?? 0) <= pausedAt)
        const held = items.length - shown.length
        return <>
          <p><button aria-pressed={pausedAt !== null} onClick={() => setPausedAt(pausedAt === null ? (items[0]?.id ?? 0) : null)}>{pausedAt === null ? 'Pause' : 'Resume'}</button>
            {' '}<span role="status">{pausedAt !== null && `${held} new event${held === 1 ? '' : 's'}`}</span></p>
          <Table label="Audit events" head={['Time', 'Actor', 'Action', 'Namespace', 'Target', 'Outcome']}>
            {shown.map((e) => <tr key={e.id}><td>{when(e.at)}</td><td>{e.actor_display ?? e.actor_id}</td><th scope="row">{e.action}</th>
              <td>{e.namespace ?? '—'}</td><td>{e.target_type ? `${e.target_type}/${e.target_id}` : '—'}</td><td>{e.outcome}</td></tr>)}
          </Table>
        </>
      }}</Q>
    </>
  )
}

export function Targets() {
  useTitle('Targets')
  const me = useMe().data!
  const q = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  if (!me.global_admin) return <><h1>Targets</h1><p>Only administrators can view this page.</p></>
  return (
    <>
      <h1>Targets</h1>
      <Q q={q}>{(d) => <Table label="Targets" head={['Target', 'Kind', 'Reachable', 'Sync', 'Version', 'Last seen', 'Last error']}>
        {d.items.map((t) => <tr key={t.name}><th scope="row">{t.name} <TierBadge tier={t.environment_tier} /></th><td>{t.kind}</td>
          <td>{t.reachable ? 'Yes' : 'No'}</td><td>{t.sync ?? '—'}</td><td>{t.server_version ?? '—'}</td><td>{hhmm(t.last_seen_at)}</td><td>{t.last_error ?? ''}</td></tr>)}</Table>}</Q>
    </>
  )
}

// My grants (requester = me) and active grants (all visible); expiry moves rows without reload.
export function Grants({ active }: { active?: boolean }) {
  useTitle(active ? 'Active grants' : 'My grants')
  const me = useMe().data!
  const now = useNow()
  const q = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  return (
    <>
      <h1>{active ? 'Active grants' : 'My grants'}</h1>
      <Q q={q}>{(d) => {
        const rows = d.items.filter((g) => active ? g.status === 'active' && remaining(g.expires_at, now) : g.requester === me.id)
        return rows.length ? <Table label="Grants" head={['Namespace', 'Target', 'Requester', 'Status', 'Access', 'Reason']}>
          {rows.map((g) => { const left = g.status === 'active' ? remaining(g.expires_at, now) : null
            return <tr key={g.id}><th scope="row">{g.namespace}</th><td>{g.target}</td><td>{g.requester}</td>
              <td>{g.status === 'active' && !left ? 'expired' : g.status}</td>
              <td>{left ? `until ${hhmm(g.expires_at)} — ${left}` : '—'}</td><td>{g.reason}</td></tr> })}
        </Table> : <p>{active ? 'No active grants.' : 'You have no grants.'}</p>
      }}</Q>
    </>
  )
}
