import { useState } from 'react'
import { Link } from 'react-router'
import { Q, remaining, Table, useGet, useMe, useNow, useTitle, when, hhmm, type S } from '../lib'
import { StatusBadge, TierBadge } from '../components/Badges'
import { Countdown, Empty, PageHeader, Pager } from '../components/Parts'
import { RevokeButton } from './Access'

const PAGE = 25
const pages = (n: number) => Math.max(1, Math.ceil(n / PAGE))

// UI audit: newest on top; pausing freezes the view at the top id seen, later events are only counted.
export function Audit() {
  useTitle('Audit')
  const [f, setF] = useState({ namespace: '', action: '', actor_id: '', grant_id: '' })
  const [outcome, setOutcome] = useState('')
  const [page, setPage] = useState(1)
  const [pausedAt, setPausedAt] = useState<number | null>(null)
  const query = Object.fromEntries(Object.entries(f).filter(([, v]) => v))
  const q = useGet<{ items: S['AuditEvent'][] }>(['audit', f.namespace, f.action, f.actor_id, f.grant_id], '/audit', { query })
  // The live dispatcher prepends every event to every cached filter variant, so filter again here.
  const match = (e: S['AuditEvent']) => (!f.namespace || e.namespace === f.namespace) && (!f.action || e.action === f.action)
    && (!f.actor_id || e.actor_id === f.actor_id) && (!f.grant_id || e.target_id === f.grant_id)
  const set = (k: keyof typeof f, v: string) => { setF({ ...f, [k]: v }); setPage(1) }
  return (
    <>
      <PageHeader title="Audit" />
      <form className="flex flex-wrap items-end gap-3 mb-2" onSubmit={(e) => e.preventDefault()} aria-label="Audit filters">
        {(['namespace', 'action', 'actor_id', 'grant_id'] as const).map((k) =>
          <label key={k} className="floating-label"><span>{k.replace('_', ' ')}</span>
            <input className="input input-sm w-40" placeholder={k.replace('_', ' ')} value={f[k]} onChange={(e) => set(k, e.target.value)} /></label>)}
      </form>
      <Q q={q} fallback="table">{(d) => {
        const items = d.items.filter(match)
        const outcomes = [...new Set(items.map((e) => e.outcome as string | undefined).filter(Boolean) as string[])]
        const view = items.filter((e) => !outcome || e.outcome === outcome)
        const shown = pausedAt === null ? view : view.filter((e) => (e.id ?? 0) <= pausedAt)
        const held = view.length - shown.length
        const p = Math.min(page, pages(shown.length))
        return <>
          <div className="flex flex-wrap items-center gap-3">
            <form className="filter" aria-label="Filter by outcome" onReset={() => { setOutcome(''); setPage(1) }}>
              <input className="btn btn-sm btn-square" type="reset" value={'×'} aria-label="Clear outcome filter" />
              {outcomes.map((o) => <input key={o} className="btn btn-sm" type="radio" name="outcome" aria-label={o} checked={outcome === o} onChange={() => { setOutcome(o); setPage(1) }} />)}
            </form>
            <button className="btn btn-sm" aria-pressed={pausedAt !== null} onClick={() => setPausedAt(pausedAt === null ? (items[0]?.id ?? 0) : null)}>{pausedAt === null ? 'Pause' : 'Resume'}</button>
            <span role="status">{pausedAt !== null && `${held} new event${held === 1 ? '' : 's'}`}</span>
          </div>
          {shown.length ? <>
            <Table label="Audit events" head={['Time', 'Actor', 'Action', 'Namespace', 'Target', 'Outcome']}>
              {shown.slice((p - 1) * PAGE, p * PAGE).map((e) => <tr key={e.id}><td>{when(e.at)}</td><td>{e.actor_display ?? e.actor_id}</td><th scope="row">{e.action}</th>
                <td>{e.namespace ?? '—'}</td><td>{e.target_type ? `${e.target_type}/${e.target_id}` : '—'}</td><td><StatusBadge text={e.outcome} /></td></tr>)}
            </Table>
            <Pager page={p} pages={pages(shown.length)} onPage={setPage} /></> : <Empty>No audit events match.</Empty>}
        </>
      }}</Q>
    </>
  )
}

// Compact chronological feed of a namespace's audit events (stack and namespace Audit tabs), newest first.
export function AuditFeed({ namespace }: { namespace: string }) {
  const [page, setPage] = useState(1)
  const q = useGet<{ items: S['AuditEvent'][] }>(['audit', namespace, '', '', ''], '/audit', { query: { namespace } })
  return <Q q={q} fallback="text">{(d) => {
    const items = d.items.filter((e) => e.namespace === namespace)
    const p = Math.min(page, pages(items.length))
    return items.length ? <>
      <ul className="timeline timeline-vertical timeline-compact timeline-snap-icon">
        {items.slice((p - 1) * PAGE, p * PAGE).map((e, i, all) => <li key={e.id}>{i > 0 && <hr />}
          <div className="timeline-middle"><span className="status status-primary" aria-hidden="true" /></div>
          <div className="timeline-end timeline-box my-2 flex flex-wrap items-center gap-2"><strong>{e.action}</strong><StatusBadge text={e.outcome} /><span className="text-sm">{e.actor_display ?? e.actor_id} {'·'} {when(e.at)}</span></div>
          {i < all.length - 1 && <hr />}</li>)}
      </ul>
      <Pager page={p} pages={pages(items.length)} onPage={setPage} /></> : <Empty>No audit events yet.</Empty>
  }}</Q>
}

export function Targets() {
  useTitle('Targets')
  const me = useMe().data!
  const q = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  if (!me.global_admin) return <><PageHeader title="Targets" /><div className="alert alert-warning"><span>Only administrators can view this page.</span></div></>
  return (
    <>
      <PageHeader title="Targets" />
      <Q q={q} fallback="table">{(d) => <Table label="Targets" head={['Target', 'Kind', 'Reachable', 'Sync', 'Version', 'Last seen', 'Last error']}>
        {d.items.map((t) => <tr key={t.name}><th scope="row">{t.name} <TierBadge tier={t.environment_tier} /></th><td>{t.kind}</td>
          <td><StatusBadge text={t.reachable ? 'Yes' : 'No'} /></td><td>{t.sync ?? '—'}</td><td>{t.server_version ?? '—'}</td><td>{hhmm(t.last_seen_at)}</td><td>{t.last_error ?? ''}</td></tr>)}</Table>}</Q>
    </>
  )
}

// Who/when for one grant: requested, decided, window, revoked.
function GrantLifecycle({ g }: { g: S['AccessGrant'] }) {
  const steps = ([[g.created_at, `Requested by ${g.requester}`], [g.decided_at, `${g.status === 'denied' ? 'Denied' : 'Approved'} by ${g.approver ?? 'an approver'}${g.decision_note ? `: ${g.decision_note}` : ''}`],
    [g.starts_at, 'Access window starts'], [g.expires_at, g.status === 'active' ? 'Access ends' : 'Access window ended'],
    [g.revoked_at, `Revoked by ${g.revoked_by ?? 'unknown'}${g.revoke_reason ? `: ${g.revoke_reason}` : ''}`]] as [string | null | undefined, string][]).filter(([at]) => at)
  return <ul className="timeline timeline-vertical timeline-compact timeline-snap-icon">
    {steps.map(([at, text], i) => <li key={text}>{i > 0 && <hr />}
      <div className="timeline-middle"><span className="status status-primary" aria-hidden="true" /></div>
      <div className="timeline-end timeline-box my-1">{text} <span className="text-sm">({when(at)})</span></div>{i < steps.length - 1 && <hr />}</li>)}
  </ul>
}

// My grants (requester = me) and active grants (all visible); expiry moves rows without reload.
export function Grants({ active }: { active?: boolean }) {
  useTitle(active ? 'Active grants' : 'My grants')
  const me = useMe().data!
  const now = useNow()
  const q = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  return (
    <>
      <PageHeader title={active ? 'Active grants' : 'My grants'} actions={<Link className="btn btn-primary btn-sm" to="/access/request">Request access</Link>} />
      <Q q={q} fallback="table">{(d) => {
        const rows = d.items.filter((g) => active ? g.status === 'active' && remaining(g.expires_at, now) : g.requester === me.id)
        return rows.length ? <>
          <Table label="Grants" head={['Namespace', 'Target', 'Requester', 'Status', 'Access', 'Time left', 'Reason', 'Actions']}>
            {rows.map((g) => { const left = g.status === 'active' ? remaining(g.expires_at, now) : null
              return <tr key={g.id}><th scope="row">{g.namespace}</th><td>{g.target}</td><td>{g.requester}</td>
                <td><StatusBadge text={g.status === 'active' && !left ? 'expired' : g.status} /></td>
                <td>{left ? `until ${hhmm(g.expires_at)} — ${left}` : '—'}</td>
                <td>{left ? <Countdown ms={Date.parse(g.expires_at!) - now} /> : '—'}</td><td>{g.reason}</td><td><RevokeButton g={g} /></td></tr> })}
          </Table>
          <h2>Lifecycle</h2>
          <div className="join join-vertical w-full">{rows.slice(0, 20).map((g) => <details key={g.id} className="collapse collapse-arrow join-item border border-base-300 bg-base-100" {...{ name: 'grant-lifecycle' }}>
            <summary className="collapse-title font-semibold">{g.id}: {g.namespace} on {g.target}</summary><div className="collapse-content"><GrantLifecycle g={g} /></div></details>)}</div></>
          : <Empty to="/access/request" cta="Request access">{active ? 'No active grants.' : 'You have no grants.'}</Empty>
      }}</Q>
    </>
  )
}
