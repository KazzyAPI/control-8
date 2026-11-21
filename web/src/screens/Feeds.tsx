import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { Q, remaining, useGet, useMe, useNow, useTitle, when, hhmm, type S } from '../lib'
import { Avatar, Countdown, EmptyState, Icon, KeyValueList, Notice, Page, PageHeader, Pager, Radial, SectionCard, StatusBadge, TierBadge, Timeline, Tip, type Accent } from '../components/ui'
import { RevokeButton } from './Access'

const PAGE = 25
const pages = (n: number) => Math.max(1, Math.ceil(n / PAGE))
const tone = (outcome?: string | null): Accent => (outcome === 'success' ? 'success' : outcome ? 'error' : 'primary')

// One audit event as a timeline entry: action, outcome, who, where, when.
const eventItem = (e: S['AuditEvent']) => ({ id: e.id ?? `${e.at}${e.action}`, tone: tone(e.outcome), content:
  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
    <Avatar name={e.actor_display ?? e.actor_id} size="sm" />
    <strong className="break-all">{e.action}</strong><StatusBadge text={e.outcome} />
    <span className="text-sm">{e.actor_display ?? e.actor_id}</span>
    {e.namespace && <span className="badge badge-ghost badge-sm">{e.namespace}</span>}
    {e.target_type && <span className="text-sm font-mono break-all">{e.target_type}/{e.target_id}</span>}
    <time className="text-sm ml-auto" dateTime={e.at ?? undefined}>{when(e.at)}</time>
  </div> })

// UI audit: newest on top; pausing freezes the view at the top id seen, later events are only counted.
export function Audit() {
  useTitle('Audit')
  const [f, setF] = useState({ namespace: '', action: '', actor_id: '', grant_id: '' })
  const [outcome, setOutcome] = useState('')
  const [page, setPage] = useState(1)
  const [pausedAt, setPausedAt] = useState<number | null>(null)
  const first = useRef<HTMLInputElement>(null)
  const query = Object.fromEntries(Object.entries(f).filter(([, v]) => v))
  const q = useGet<{ items: S['AuditEvent'][] }>(['audit', f.namespace, f.action, f.actor_id, f.grant_id], '/audit', { query })
  // "/" jumps to the first filter (unless you are already typing somewhere).
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === '/' && !/^(INPUT|TEXTAREA|SELECT)$/.test((e.target as HTMLElement).tagName)) { e.preventDefault(); first.current?.focus() } }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [])
  // The live dispatcher prepends every event to every cached filter variant, so filter again here.
  const match = (e: S['AuditEvent']) => (!f.namespace || e.namespace === f.namespace) && (!f.action || e.action === f.action)
    && (!f.actor_id || e.actor_id === f.actor_id) && (!f.grant_id || e.target_id === f.grant_id)
  const set = (k: keyof typeof f, v: string) => { setF({ ...f, [k]: v }); setPage(1) }
  return (
    <Page>
      <PageHeader icon="list" title="Audit" subtitle={<>Every state-changing action, newest first, live. Press <kbd className="kbd kbd-xs">/</kbd> to filter.</>} />
      <SectionCard icon="search" title="Filters">
        <form className="flex flex-wrap items-end gap-3" onSubmit={(e) => e.preventDefault()} aria-label="Audit filters">
          {(['namespace', 'action', 'actor_id', 'grant_id'] as const).map((k, i) =>
            <label key={k} className="floating-label"><span>{k.replace('_', ' ')}</span>
              <input ref={i === 0 ? first : undefined} className="input input-sm w-40" placeholder={k.replace('_', ' ')} value={f[k]} onChange={(e) => set(k, e.target.value)} /></label>)}
        </form>
      </SectionCard>
      <Q q={q} fallback="cards">{(d) => {
        const items = d.items.filter(match)
        const outcomes = [...new Set(items.map((e) => e.outcome as string | undefined).filter(Boolean) as string[])]
        const view = items.filter((e) => !outcome || e.outcome === outcome)
        const shown = pausedAt === null ? view : view.filter((e) => (e.id ?? 0) <= pausedAt)
        const held = view.length - shown.length
        const p = Math.min(page, pages(shown.length))
        return <SectionCard icon="bolt" title="Events" actions={<>
          <form className="filter" aria-label="Filter by outcome" onReset={() => { setOutcome(''); setPage(1) }}>
            <input className="btn btn-sm btn-square" type="reset" value={'×'} aria-label="Clear outcome filter" />
            {outcomes.map((o) => <input key={o} className="btn btn-sm" type="radio" name="outcome" aria-label={o} checked={outcome === o} onChange={() => { setOutcome(o); setPage(1) }} />)}
          </form>
          <Tip tip={pausedAt === null ? 'Hold the list still while you read; new events are counted.' : 'Show the new events'} pos="left">
            <button className={`btn btn-sm ${pausedAt !== null ? 'btn-warning' : ''}`} aria-pressed={pausedAt !== null} onClick={() => setPausedAt(pausedAt === null ? (items[0]?.id ?? 0) : null)}>
              <Icon name={pausedAt === null ? 'pause' : 'play'} className="size-4" />{pausedAt === null ? 'Pause' : 'Resume'}</button></Tip>
          <span role="status" className="text-sm">{pausedAt !== null && `${held} new event${held === 1 ? '' : 's'}`}</span></>}>
          {shown.length ? <>
            <Timeline label="Audit events" items={shown.slice((p - 1) * PAGE, p * PAGE).map(eventItem)} />
            <Pager page={p} pages={pages(shown.length)} onPage={setPage} /></> : <EmptyState icon="search" hint="Try clearing a filter.">No audit events match.</EmptyState>}
        </SectionCard>
      }}</Q>
    </Page>
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
      <Timeline label="Audit events" items={items.slice((p - 1) * PAGE, p * PAGE).map(eventItem)} />
      <Pager page={p} pages={pages(items.length)} onPage={setPage} /></> : <EmptyState icon="list" hint="Actions in this namespace are recorded here.">No audit events yet.</EmptyState>
  }}</Q>
}

export function Targets() {
  useTitle('Targets')
  const me = useMe().data!
  const q = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  if (!me.global_admin) return <Page><PageHeader icon="server" title="Targets" /><Notice tone="warning">Only administrators can view this page.</Notice></Page>
  return (
    <Page>
      <PageHeader icon="server" title="Targets" subtitle="Clusters Control 8 can deploy to. Targets are configured through the environment (C8_TARGETS), never in the UI." />
      <Q q={q} fallback="cards">{(d) => d.items.length ? <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{d.items.map((t) =>
        <li key={t.name} className={`card card-border bg-base-100 border-t-4 ${t.reachable ? 'border-t-success' : 'border-t-error'}`}><div className="card-body p-5 gap-3">
          <div className="flex items-start gap-3">
            <span className="grid place-items-center size-10 rounded-box bg-primary/10 text-primary"><Icon name="server" /></span>
            <div className="min-w-0 flex-1"><h2 className="card-title text-base break-words">{t.name}</h2><div className="mt-1 flex flex-wrap gap-2"><TierBadge tier={t.environment_tier} /><StatusBadge text={t.reachable ? 'reachable' : 'unreachable'} /></div></div>
          </div>
          <KeyValueList cols={2} items={[{ label: 'Kind', value: t.kind }, { label: 'Sync', value: t.sync ?? '—' }, { label: 'Version', value: t.server_version ?? '—' }, { label: 'Last seen', value: hhmm(t.last_seen_at) }]} />
          {t.last_error && <Notice tone="error" title="Last error"><span className="font-mono break-all">{t.last_error}</span></Notice>}
        </div></li>)}</ul> : <EmptyState icon="server" hint="Set C8_TARGETS in the Control 8 deployment.">No targets configured.</EmptyState>}</Q>
    </Page>
  )
}

// Who/when for one grant: requested, decided, window, revoked.
function GrantLifecycle({ g }: { g: S['AccessGrant'] }) {
  const steps = ([[g.created_at, `Requested by ${g.requester}`, 'primary'], [g.decided_at, `${g.status === 'denied' ? 'Denied' : 'Approved'} by ${g.approver ?? 'an approver'}${g.decision_note ? `: ${g.decision_note}` : ''}`, g.status === 'denied' ? 'error' : 'success'],
    [g.starts_at, 'Access window starts', 'primary'], [g.expires_at, g.status === 'active' ? 'Access ends' : 'Access window ended', 'warning'],
    [g.revoked_at, `Revoked by ${g.revoked_by ?? 'unknown'}${g.revoke_reason ? `: ${g.revoke_reason}` : ''}`, 'error']] as [string | null | undefined, string, Accent][]).filter(([at]) => at)
  return <Timeline label={`Lifecycle of ${g.id}`} items={steps.map(([at, text, t]) => ({ id: text, tone: t, content: <>{text} <span className="text-sm">({when(at)})</span></> }))} />
}

// My grants (requester = me) and active grants (all visible); expiry moves cards without reload.
export function Grants({ active }: { active?: boolean }) {
  useTitle(active ? 'Active grants' : 'My grants')
  const me = useMe().data!
  const now = useNow()
  const q = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  return (
    <Page>
      <PageHeader icon={active ? 'bolt' : 'key'} title={active ? 'Active grants' : 'My grants'}
        subtitle={active ? 'Prod access that is in effect right now.' : 'Your requests for time-boxed prod access, and how each one went.'}
        actions={<Link className="btn btn-primary btn-sm" to="/access/request"><Icon name="plus" className="size-4" />Request access</Link>} />
      <Q q={q} fallback="cards">{(d) => {
        const rows = d.items.filter((g) => active ? g.status === 'active' && remaining(g.expires_at, now) : g.requester === me.id)
        return rows.length ? <ul className="grid gap-4 lg:grid-cols-2" aria-label="Grants">
          {rows.map((g) => { const left = g.status === 'active' ? remaining(g.expires_at, now) : null
            const ms = g.expires_at ? Date.parse(g.expires_at) - now : 0
            const total = (g.duration_minutes ?? 0) * 60000
            const pct = total ? (ms / total) * 100 : 100
            return <li key={g.id} className="card card-border bg-base-100"><div className="card-body p-4 gap-3">
              <div className="flex items-start gap-3">
                <Avatar name={g.requester} />
                <div className="min-w-0 flex-1"><h2 className="card-title text-base break-words">{g.namespace} on {g.target}</h2><p className="text-sm">{g.requester} {'·'} <span className="font-mono">{g.id}</span></p></div>
                <StatusBadge text={g.status === 'active' && !left ? 'expired' : g.status} />
              </div>
              <p className="text-sm bg-base-200 rounded-box p-3 break-words">{g.reason}</p>
              {left && <div className="flex items-center gap-3">
                <Radial value={pct} tone={pct < 15 ? 'error' : pct < 40 ? 'warning' : 'success'} label={`Time left on grant ${g.id}`} />
                <div><p className="text-sm">Access until {hhmm(g.expires_at)} {'—'} {left}</p><Countdown ms={ms} className="text-xl" /></div>
              </div>}
              <div className="flex flex-wrap justify-end gap-2"><RevokeButton g={g} /></div>
              <details className="collapse collapse-arrow bg-base-200"><summary className="collapse-title text-sm font-semibold min-h-0 py-2">Lifecycle</summary><div className="collapse-content"><GrantLifecycle g={g} /></div></details>
            </div></li> })}
        </ul>
          : <EmptyState icon="key" to="/access/request" cta="Request access" hint="Prod changes need an approved, time-boxed grant.">{active ? 'No active grants.' : 'You have no grants.'}</EmptyState>
      }}</Q>
    </Page>
  )
}
