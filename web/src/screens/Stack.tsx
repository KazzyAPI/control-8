import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { ProfileBadge, StatusBadge, TierBadge } from '../components/Badges'
import { Empty, MockCode, PageHeader, Pager, ProdRibbon, Radial, Stat, Stats, Tabs } from '../components/Parts'
import { TargetNote } from '../components/Rows'
import { get, hhmm, profileTag, Q, remaining, sumReady, Table, useFlash, useGet, useProdAccess, useTitle, when, type S } from '../lib'
import { DeploymentActions } from './Access'
import { AuditFeed } from './Feeds'
import { DeployDialog, StackActions } from './Write'

const Workload = ({ s }: { s: S['Service'] }) => {
  const n = useFlash(JSON.stringify([s.ready_replicas, s.desired_replicas, s.image_ref, s.state]))
  const [r, d] = [s.ready_replicas ?? 0, s.desired_replicas ?? 0]
  return (
    <tr key={n} className={n ? 'flash' : ''}>
      <th scope="row">{s.kind}/{s.name}</th>
      <td>{r}/{d} <progress className={`progress w-16 align-middle ${r < d ? 'progress-warning' : 'progress-success'}`} value={r} max={Math.max(d, 1)} aria-hidden="true" /></td>
      <td>{s.image_ref ?? '—'}</td>
      <td>{s.image_id ?? '—'}</td>
    </tr>
  )
}

export const DeploymentRows = ({ items }: { items: S['Deployment'][] }) => (
  <Table label="Deployments" head={['Deployment', 'Revision', 'Safety profile', 'Status', 'Requested by']}>
    {items.map((d) => <tr key={d.id}>
      <th scope="row"><Link to={`/deployments/${d.id}`}>{d.id}</Link></th>
      <td>{d.revision ?? '—'}</td><td><ProfileBadge profile={d.safety_profile} /></td><td><StatusBadge text={d.status} /></td><td>{d.requested_by}</td></tr>)}
  </Table>
)

// Deployment history as a compact vertical timeline (newest first).
const DeploymentHistory = ({ items }: { items: S['Deployment'][] }) => (
  <ul className="timeline timeline-vertical timeline-compact timeline-snap-icon">
    {items.map((d, i) => <li key={d.id}>
      {i > 0 && <hr />}
      <div className="timeline-middle"><span className="status status-primary" aria-hidden="true" /></div>
      <div className="timeline-end timeline-box flex flex-wrap items-center gap-2 my-2"><Link to={`/deployments/${d.id}`}>{d.id}</Link> <StatusBadge text={d.status} /> <span>revision {d.revision ?? '—'} by {d.requested_by}</span></div>
      {i < items.length - 1 && <hr />}
    </li>)}
  </ul>
)

// UI-016: live grant state for a prod stack.
export function GrantState({ s }: { s: S['Stack'] }) {
  const a = useProdAccess(s.namespace!, s.target!, s.environment_tier)
  if (a.state === 'open') return null
  if (a.state === 'bypass') return <div className="alert alert-warning my-2"><span>Grant bypassed (break-glass)</span></div>
  if (a.state === 'active') return <div className="alert alert-success my-2"><span>Access until {hhmm(a.active!.expires_at)} {'—'} {remaining(a.active!.expires_at, a.now)}</span></div>
  if (a.state === 'pending') return <div className="alert alert-info my-2"><span>Awaiting approval</span></div>
  return <div className="alert my-2"><span><Link to={`/access/request?namespace=${s.namespace}&target=${s.target}`}>Request prod access</Link></span></div>
}

type StackTab = 'overview' | 'revisions' | 'deployments' | 'audit' | 'settings'
const REV_PAGE = 10

export function Stack() {
  const { ns = '', stack = '' } = useParams()
  const [tab, setTab] = useState<StackTab>('overview')
  const [revPage, setRevPage] = useState(1)
  const q = useGet<S['Stack']>(['stack', ns, stack], `/namespaces/${ns}/stacks/${stack}`, { path: { ns, stack } })
  const deps = useGet<{ items: S['Deployment'][] }>(['deployments', ns, stack], `/namespaces/${ns}/stacks/${stack}/deployments`, { path: { ns, stack } })
  const revs = useGet<{ items: S['Revision'][] }>(['revisions', ns, stack], `/namespaces/${ns}/stacks/${stack}/revisions`, { path: { ns, stack } })
  const s = q.data
  // UI-012
  useTitle(s ? `${stack} — ${ns} [${s.environment_tier === 'prod' ? 'PROD' : 'NON-PROD'}] ${profileTag(s.safety_profile)}`.trim() : stack)
  const running = (deps.data?.items ?? []).find((d) => d.status === 'running' && d.rollout?.workloads?.length)
  return (
    <Q q={q} fallback="page">{(s) => { const [r, d] = sumReady(s); return <>
      <ProdRibbon tier={s.environment_tier} />
      <PageHeader title={s.slug} badges={<><TierBadge tier={s.environment_tier} large /><ProfileBadge profile={s.safety_profile} large /></>}
        actions={<Link className="btn btn-primary btn-sm" to={`/n/${ns}/s/${stack}/revisions/new`}>New revision</Link>} />
      <TargetNote target={s.target} />
      {s.rollout_frozen && <div className="alert alert-error my-2" role="alert"><span>Automatic updates stopped after a failed rollback {'—'} an admin must deploy a revision</span></div>}
      <GrantState s={s} />
      <Stats>
        <Stat title="Health" value={<StatusBadge text={s.health ?? 'unknown'} />} desc={`state ${s.state}`} />
        <Stat title="Ready" value={`${r}/${d}`} desc="replicas ready" figure={<Radial value={d ? (r / d) * 100 : 0} label="Replicas ready" />} />
        <Stat title="Target" value={s.target ?? '—'} desc={s.runtime} />
        <Stat title="Desired revision" value={s.desired_revision ?? '—'} desc="deployed on apply" />
      </Stats>
      <Tabs label="Stack sections" value={tab} onChange={setTab} tabs={[['overview', 'Overview'], ['revisions', 'Revisions'], ['deployments', 'Deployments'], ['audit', 'Audit'], ['settings', 'Settings']]}>
        {tab === 'overview' && <>
          <h2 className="mt-0">Workloads</h2>
          {s.services?.length ? <Table label="Workloads" head={['Workload', 'Ready', 'Image', 'Digest']}>{s.services.map((v) => <Workload key={`${v.kind}/${v.name}`} s={v} />)}</Table> : <Empty>No workloads observed.</Empty>}
          {running && <section aria-labelledby="h-roll"><h2 id="h-roll">Rollout in progress</h2>
            {running.rollout!.workloads!.map((w) => <p key={`${w.kind}/${w.name}`}>
              <label className="flex flex-wrap items-center gap-2">{w.kind}/{w.name} {w.ready ?? 0}/{w.desired ?? 0} <progress className="progress progress-primary w-56" value={w.ready ?? 0} max={Math.max(w.desired ?? 0, 1)} /></label></p>)}</section>}
          <h2>Drift</h2>
          {s.drift?.length ? <ul className="list bg-base-100 rounded-box border border-base-300">{s.drift.map((x, i) => <li key={i} className="list-row"><span className="badge badge-warning">drift</span><span>{x.kind}: {x.message}</span></li>)}</ul> : <p>No drift.</p>}
          <h2>Digest updates available</h2>
          {s.digest_status?.updates?.length ? <ul className="list bg-base-100 rounded-box border border-base-300">{s.digest_status.updates.map((u, i) => <li key={i} className="list-row">{u.workload}/{u.container}: {u.current ?? 'unknown'} {'→'} {u.available}</li>)}</ul> : <p>None{s.digest_status?.checked_at && <> (checked {when(s.digest_status.checked_at)})</>}.</p>}
          <h2>Recent activity</h2>
          <Q q={deps}>{(v) => v.items.length ? <DeploymentHistory items={v.items.slice(0, 5)} /> : <p>No deployments yet.</p>}</Q>
        </>}
        {tab === 'revisions' && <>
          <h2 className="mt-0">Revisions</h2>
          <Q q={revs} fallback="table">{(v) => v.items.length ? <>
            <ul className="list bg-base-100 rounded-box border border-base-300">{v.items.slice((revPage - 1) * REV_PAGE, revPage * REV_PAGE).map((x) =>
              <li key={x.number} className="list-row items-center"><div><Link to={`/n/${ns}/s/${stack}/revisions/${x.number}`}>Revision {x.number}</Link></div><ProfileBadge profile={x.safety_profile} /></li>)}</ul>
            <Pager page={revPage} pages={Math.ceil(v.items.length / REV_PAGE)} onPage={setRevPage} /></>
            : <Empty to={`/n/${ns}/s/${stack}/revisions/new`} cta="Create the first revision">No revisions yet.</Empty>}</Q>
        </>}
        {tab === 'deployments' && <>
          <h2 className="mt-0">Recent deployments</h2>
          <Q q={deps} fallback="table">{(v) => v.items.length ? <DeploymentRows items={v.items.slice(0, 10)} /> : <Empty>No deployments yet.</Empty>}</Q>
        </>}
        {tab === 'audit' && <AuditFeed namespace={ns} />}
        {tab === 'settings' && <StackActions s={s} />}
      </Tabs>
    </> }}</Q>
  )
}

// Two manifests side by side with the daisyUI diff slider (text only, UI-003).
function DiffView({ a, b, an, bn }: { a: string; b: string; an: number; bn: number }) {
  if (a === b) return <p>No changes between revision {an} and revision {bn}.</p>
  const pane = (t: string) => <div className="bg-base-200 text-base-content p-4 overflow-auto"><pre className="text-sm">{t}</pre></div>
  return <>
    <p className="text-sm">Drag the handle to wipe between revision {an} (left) and revision {bn} (right).</p>
    <figure className="diff aspect-16/9 rounded-box border border-base-300" role="group" aria-label={`Manifest diff, revision ${an} against revision ${bn}`}>
      <div className="diff-item-1">{pane(a)}</div><div className="diff-item-2">{pane(b)}</div><div className="diff-resizer" />
    </figure>
  </>
}

export function Revision() {
  const { ns = '', stack = '', n = '' } = useParams()
  const q = useGet<S['Revision']>(['revisions', ns, stack, n], `/namespaces/${ns}/stacks/${stack}/revisions/${n}`, { path: { ns, stack, n: Number(n) } })
  const st = useGet<S['Stack']>(['stack', ns, stack], `/namespaces/${ns}/stacks/${stack}`, { path: { ns, stack } })
  const [deploying, setDeploying] = useState(false)
  const access = useProdAccess(ns, st.data?.target ?? '', st.data?.environment_tier)
  const base = q.data?.base_revision ?? (Number(n) > 1 ? Number(n) - 1 : null) // diff against the revision this one was based on
  const prev = useQuery({ queryKey: ['revisions', ns, stack, String(base)], enabled: base != null && q.data !== undefined,
    queryFn: () => get<S['Revision']>(`/namespaces/${ns}/stacks/${stack}/revisions/${base}`, { path: { ns, stack, n: base } }) })
  useTitle(`Revision ${n} — ${stack}`)
  const text = (r?: S['Revision']) => r?.manifests_input ?? r?.compose_input ?? ''
  const acc = (title: string, children: React.ReactNode, open = false) =>
    <details className="collapse collapse-arrow join-item border border-base-300 bg-base-100" open={open}>
      <summary className="collapse-title text-lg font-semibold">{title}</summary><div className="collapse-content">{children}</div></details>
  return (
    <Q q={q} fallback="page">{(r) => <>
      <ProdRibbon tier={st.data?.environment_tier} />
      <PageHeader title={`Revision ${r.number}`} badges={<ProfileBadge profile={r.safety_profile} large />}
        actions={st.data && <button className="btn btn-primary btn-sm" disabled={!access.allowed} onClick={() => setDeploying(true)}>Deploy revision {r.number}</button>} />
      {st.data && <>
        <GrantState s={st.data} />
        {deploying && <DeployDialog s={st.data} r={r} onClose={() => setDeploying(false)} />}</>}
      <p>origin {r.origin} {'·'} created {when(r.created_at)} by {r.created_by}</p>
      <p><Link to={`/n/${ns}/s/${stack}`}>Back to {stack}</Link></p>
      <div className="join join-vertical w-full">
        {acc('Policy result', <>
          {r.policy_result?.violations?.length ? <ul className="list">{r.policy_result.violations.map((v, i) => <li key={i} className="list-row"><span className="badge badge-error">violation</span><span>{v.rule}: {v.message}</span></li>)}</ul> : <p>{r.policy_result?.allowed === false ? 'Not allowed.' : 'No violations.'}</p>}
          {r.policy_result?.approval_reasons?.length ? <div className="alert alert-warning"><span>Needs approval: {r.policy_result.approval_reasons.join('; ')}</span></div> : null}</>, true)}
        {acc('Objects', r.inventory?.length ? <ul className="list">{r.inventory.map((o, i) => <li key={i} className="list-row">{o.kind}/{o.name}</li>)}</ul> : <p>None.</p>)}
        {acc('Manifests', <MockCode text={text(r)} label="Manifests" />, true)}
        {base != null && acc(`Changes since revision ${base}`, <Q q={prev}>{(p) => <DiffView a={text(p)} b={text(r)} an={base} bn={r.number!} />}</Q>)}
      </div>
    </>}</Q>
  )
}

// Deployment lifecycle: steps for the state, a timeline for who/when.
const STATE_STEP: Record<string, number> = { queued: 0, running: 1, succeeded: 2, failed: 2 }
export function Deployment() {
  const { id = '' } = useParams()
  const q = useGet<S['Deployment']>(['deployment', id], `/deployments/${id}`, { path: { id } })
  const status = q.data?.status
  const out = useQuery({ queryKey: ['deployment-output', id, status], enabled: !!status, queryFn: () => get<S['Deployment']>(`/deployments/${id}`, { path: { id } }) })
  useTitle(`Deployment ${id}`)
  return (
    <Q q={q} fallback="page">{(d) => {
      const at = STATE_STEP[d.status ?? ''] ?? -1
      const last = d.status === 'failed' ? 'Failed' : 'Succeeded'
      const roll = d.rollout?.state === 'rolling_back' || d.rollout?.result === 'rolled_back' || d.rollout?.result === 'rollback_failed'
      return <>
        <ProdRibbon tier={d.environment_tier} />
        <PageHeader title={`Deployment ${d.id}`} badges={<><TierBadge tier={d.environment_tier} large /><ProfileBadge profile={d.safety_profile} large /></>} />
        <p>Status: <StatusBadge text={d.status} />
          {d.grant_bypass && <> {'·'} grant bypassed (break-glass)</>}{d.grant_id && <> {'·'} grant {d.grant_id}</>}</p>
        <ul className="steps w-full my-4" aria-label="Deployment state">
          {['Queued', 'Running', last].map((t, i) => <li key={t} className={`step ${i <= at ? (i === 2 && d.status === 'failed' ? 'step-error' : i === 2 ? 'step-success' : 'step-primary') : ''}`} aria-current={i === at ? 'step' : undefined}>{t}</li>)}
        </ul>
        {at < 0 && <p>This deployment is {d.status?.replace('_', ' ')}.</p>}
        {roll && <>
          <div className="alert alert-warning my-2" role="status"><span>{d.rollout?.state === 'rolling_back' ? `Rolling back to revision ${d.rollout.rolled_back_to ?? '?'}` : `Rollout rolled back to revision ${d.rollout?.rolled_back_to ?? '?'}`}</span></div>
          <ul className="steps w-full my-4" aria-label="Rollback state">
            <li className="step step-error">Rollout failed</li>
            <li className={`step ${d.rollout?.state === 'rolling_back' ? 'step-warning' : 'step-primary'}`}>Rolling back</li>
            <li className={`step ${d.rollout?.result === 'rolled_back' ? 'step-success' : d.rollout?.result === 'rollback_failed' ? 'step-error' : ''}`}>{d.rollout?.result === 'rollback_failed' ? 'Rollback failed' : 'Restored'}</li>
          </ul></>}
        {d.rollout?.result && <p>Rollout result: {d.rollout.result}</p>}
        <h2>Timeline</h2>
        <ul className="timeline timeline-vertical timeline-compact timeline-snap-icon">
          {([[`Requested by ${d.requested_by}${d.reason ? `: ${d.reason}` : ''}`, null], [d.approved_by && `Approved by ${d.approved_by}`, null], [d.started_at && `Started ${when(d.started_at)}`, null],
            [d.finished_at && `Finished ${when(d.finished_at)} — ${d.status}`, d.status === 'succeeded' ? 'success' : 'error']] as [string | null | undefined, string | null][])
            .filter(([t]) => t).map(([t, tone], i, all) => <li key={t}>{i > 0 && <hr />}
              <div className="timeline-middle"><span className={`status ${tone === 'success' ? 'status-success' : tone === 'error' ? 'status-error' : 'status-primary'}`} aria-hidden="true" /></div>
              <div className="timeline-end timeline-box my-2">{t}</div>{i < all.length - 1 && <hr />}</li>)}
        </ul>
        <DeploymentActions d={d} />
        {d.error && <div className="alert alert-error my-2" role="alert"><span>Error: {d.error}</span></div>}
        <h2>Rollout</h2>
        {d.rollout?.workloads?.length ? <Table label="Rollout" head={['Workload', 'Desired', 'Updated', 'Ready', 'Available', 'Reason']}>
          {d.rollout.workloads.map((w) => <tr key={`${w.kind}/${w.name}`}><th scope="row">{w.kind}/{w.name}</th><td>{w.desired}</td><td>{w.updated}</td>
            <td>{w.ready} <progress className="progress progress-primary w-16 align-middle" value={w.ready ?? 0} max={Math.max(w.desired ?? 0, 1)} aria-hidden="true" /></td><td>{w.available}</td><td>{w.reason ?? ''}</td></tr>)}</Table> : <p>No rollout data.</p>}
        <details className="collapse collapse-arrow bg-base-200 mt-4" open>
          <summary className="collapse-title text-xl font-semibold">Output</summary>
          <div className="collapse-content"><MockCode text={out.data?.output ?? ''} label="Deployment output" prefix="$" /></div></details>
      </>
    }}</Q>
  )
}
