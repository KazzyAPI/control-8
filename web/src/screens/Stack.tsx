import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { healthOf, ReadyBar, TargetNote } from '../components/Rows'
import { Avatar, DataTable, DiffView, EmptyState, Icon, KeyValueList, MockCode, Notice, Page, PageHeader, Pager, ProfileBadge, Radial, SectionCard, StatusBadge, Stepper, Tabs, TierBadge, TierBanner, Timeline,
  Tip, type Accent, type Column, type StepState } from '../components/ui'
import { get, hhmm, profileTag, Q, remaining, sumReady, useGet, useProdAccess, useTitle, when, type S } from '../lib'
import { DeploymentActions } from './Access'
import { AuditFeed } from './Feeds'
import { DeployDialog, StackActions } from './Write'

// "registry/repo:tag" -> [repo, tag]; digests and missing tags fall back to latest/none.
const splitImage = (ref?: string | null): [string, string] => {
  if (!ref) return ['No image yet', '']
  const at = ref.indexOf('@'); if (at >= 0) return [ref.slice(0, at), ref.slice(at + 1, at + 13)]
  const i = ref.lastIndexOf(':')
  return i > ref.lastIndexOf('/') ? [ref.slice(0, i), ref.slice(i + 1)] : [ref, 'latest']
}

const WORKLOAD_COLUMNS: Column<S['Service']>[] = [
  { key: 'w', header: 'Workload', rowHeader: true, cell: (s) => <>{s.kind}/{s.name}{s.is_database && <span className="badge badge-ghost badge-sm ml-2">database</span>}</> },
  { key: 'h', header: 'Status', cell: (s) => { const [r, d] = [s.ready_replicas ?? 0, s.desired_replicas ?? 0]; return <StatusBadge text={s.health ?? (d && r >= d ? 'healthy' : 'degraded')} /> } },
  { key: 'r', header: 'Ready', cell: (s) => <ReadyBar r={s.ready_replicas ?? 0} d={s.desired_replicas ?? 0} /> },
  { key: 'i', header: 'Image', hideBelow: 'md', cell: (s) => <span className="font-mono text-xs break-all">{s.image_ref ?? '—'}</span> },
  { key: 'g', header: 'Digest', hideBelow: 'lg', cell: (s) => <span className="font-mono text-xs break-all">{s.image_id ?? '—'}</span> },
]

const DEP_COLUMNS: Column<S['Deployment']>[] = [
  { key: 'id', header: 'Deployment', rowHeader: true, cell: (d) => <Link to={`/deployments/${d.id}`}>{d.id}</Link> },
  { key: 'rev', header: 'Revision', cell: (d) => d.revision ?? '—' },
  { key: 'p', header: 'Safety profile', hideBelow: 'md', cell: (d) => <ProfileBadge profile={d.safety_profile} /> },
  { key: 's', header: 'Status', cell: (d) => <StatusBadge text={d.status} /> },
  { key: 'by', header: 'Requested by', hideBelow: 'sm', cell: (d) => <span className="inline-flex items-center gap-2"><Avatar name={d.requested_by} size="sm" />{d.requested_by}</span> },
]
export const DeploymentRows = ({ items }: { items: S['Deployment'][] }) => <DataTable label="Deployments" columns={DEP_COLUMNS} rows={items} rowKey={(d) => d.id} />

const depTone = (s?: string | null): Accent => (s === 'succeeded' ? 'success' : s === 'failed' || s === 'rejected' ? 'error' : s === 'pending_approval' ? 'warning' : 'primary')
// Deployment history as a compact vertical timeline (newest first).
const DeploymentHistory = ({ items }: { items: S['Deployment'][] }) => (
  <Timeline label="Recent deployments" items={items.map((d) => ({ id: d.id, tone: depTone(d.status),
    content: <div className="flex flex-wrap items-center gap-2"><Link to={`/deployments/${d.id}`} className="font-medium">{d.id}</Link> <StatusBadge text={d.status} /> <span className="text-sm">revision {d.revision ?? '—'} by {d.requested_by}</span></div> }))} />
)

// UI-016: live grant state for a prod stack.
export function GrantState({ s }: { s: S['Stack'] }) {
  const a = useProdAccess(s.namespace!, s.target!, s.environment_tier)
  if (a.state === 'open') return null
  if (a.state === 'bypass') return <Notice tone="warning">Grant bypassed (break-glass)</Notice>
  if (a.state === 'active') return <Notice tone="success" title="Prod access active">Access until {hhmm(a.active!.expires_at)} {'—'} {remaining(a.active!.expires_at, a.now)}</Notice>
  if (a.state === 'pending') return <Notice tone="info">Awaiting approval</Notice>
  return <Notice tone="info" action={<Link className="btn btn-sm btn-primary" to={`/access/request?namespace=${s.namespace}&target=${s.target}`}><Icon name="key" className="size-4" />Request prod access</Link>}>Changes to this prod stack need an access grant.</Notice>
}

type StackTab = 'overview' | 'revisions' | 'deployments' | 'audit' | 'settings'
const REV_PAGE = 10
const ROLL_STEPS = ['Apply', 'Roll out', 'Health check', 'Done']
const rollSteps = (st?: string | null) => { const at = st === 'done' ? 4 : st === 'waiting' || st === 'rolling_out' ? 2 : 1
  return ROLL_STEPS.map((label, i): { label: string; state: StepState } => ({ label, state: i < at - 1 ? 'done' : i === at - 1 ? 'current' : 'todo' })) }

// Hero summary: what is running (image and version), how healthy it is, and where.
function Summary({ s }: { s: S['Stack'] }) {
  const [r, d] = sumReady(s)
  const [repo, tag] = splitImage(s.services?.find((v) => v.image_ref)?.image_ref)
  return (
    <section aria-label="Summary" className="card card-border bg-base-100 shadow-sm"><div className="card-body gap-5 lg:flex-row lg:items-center">
      <div className="flex items-center gap-4 flex-1 min-w-0">
        <span className="grid place-items-center size-14 shrink-0 rounded-box bg-primary text-primary-content"><Icon name="cube" className="size-8" /></span>
        <div className="min-w-0"><p className="text-xs uppercase tracking-wide">Running image</p>
          <p className="font-mono font-semibold break-all">{repo}</p>
          {tag && <span className="badge badge-primary mt-1">{tag}</span>}</div>
      </div>
      <div className="flex items-center gap-4">
        <Radial value={d ? (r / d) * 100 : 0} size="5rem" tone={d && r < d ? 'warning' : 'success'} label="Replicas ready" />
        <div><StatusBadge text={healthOf(s)} /><p className="text-sm mt-1">{r}/{d} replicas ready</p></div>
      </div>
      <div className="lg:border-l lg:border-base-300 lg:pl-6"><KeyValueList items={[
        { label: 'Target', value: <>{s.target} <span className="text-sm font-normal">({s.runtime})</span></>, icon: 'server' },
        { label: 'Desired revision', value: s.desired_revision ? <Link to={`/n/${s.namespace}/s/${s.slug}/revisions/${s.desired_revision}`}>Revision {s.desired_revision}</Link> : '—', icon: 'document' },
        { label: 'State', value: s.state, icon: 'bolt' }]} /></div>
    </div></section>
  )
}

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
    <Q q={q} fallback="page">{(s) => <Page>
      <PageHeader icon="layers" title={s.slug} subtitle={<>{s.asset && <><Link to={`/n/${ns}/assets/${s.asset}`}>{s.asset}</Link> {'·'} </>}<Link to={`/n/${ns}`}>{ns}</Link> on {s.target}</>}
        badges={<><TierBadge tier={s.environment_tier} large /><ProfileBadge profile={s.safety_profile} large /></>}
        actions={<Link className="btn btn-primary btn-sm" to={`/n/${ns}/s/${stack}/revisions/new`}><Icon name="plus" className="size-4" />New revision</Link>} />
      <TierBanner tier={s.environment_tier} />
      <TargetNote target={s.target} />
      {s.rollout_frozen && <Notice tone="error" role="alert" title="Automatic updates stopped after a failed rollback">An admin must deploy a revision to continue.</Notice>}
      <GrantState s={s} />
      <Summary s={s} />
      <Tabs label="Stack sections" value={tab} onChange={setTab} tabs={[['overview', 'Overview', 'home'], ['revisions', 'Revisions', 'document'], ['deployments', 'Deployments', 'deploy'], ['audit', 'Audit', 'list'], ['settings', 'Settings', 'sliders']]}>
        {tab === 'overview' && <>
          <SectionCard flush icon="cube" title="Workloads" subtitle="Live from the cluster">
            <div className="px-5 pb-5">{s.services?.length ? <DataTable label="Workloads" columns={WORKLOAD_COLUMNS} rows={s.services} rowKey={(v) => `${v.kind}/${v.name}`} rowSig={(v) => JSON.stringify([v.ready_replicas, v.desired_replicas, v.image_ref, v.state])} />
              : <EmptyState icon="cube" hint="Workloads appear once the first revision is deployed.">No workloads observed.</EmptyState>}</div>
          </SectionCard>
          {running && <SectionCard icon="refresh" title="Rollout in progress" subtitle={`Deployment ${running.id}`}>
            <Stepper label="Rollout steps" steps={rollSteps(running.rollout?.state)} />
            <div className="grid gap-3 sm:grid-cols-2">{running.rollout!.workloads!.map((w) => <label key={`${w.kind}/${w.name}`} className="flex flex-col gap-1 text-sm">
              <span className="flex justify-between"><span>{w.kind}/{w.name}</span><span>{w.ready ?? 0}/{w.desired ?? 0}</span></span>
              <progress className="progress progress-primary w-full" value={w.ready ?? 0} max={Math.max(w.desired ?? 0, 1)} /></label>)}</div>
          </SectionCard>}
          <div className="grid gap-5 lg:grid-cols-2">
            <SectionCard icon="drift" title="Drift" subtitle="Where the cluster differs from the desired revision">
              {s.drift?.length ? <div className="flex flex-col gap-2">{s.drift.map((x, i) => <Notice key={i} tone="warning" title={x.kind}>{x.message}</Notice>)}</div>
                : <Notice tone="success">No drift. The cluster matches the desired revision.</Notice>}
            </SectionCard>
            <SectionCard icon="refresh" title="Digest updates available">
              {s.digest_status?.updates?.length ? <div className="flex flex-col gap-2">{s.digest_status.updates.map((u, i) => <Notice key={i} tone="info">{u.workload}/{u.container}: {u.current ?? 'unknown'} {'→'} {u.available}</Notice>)}</div>
                : <Notice tone="info">None{s.digest_status?.checked_at && <> (checked {when(s.digest_status.checked_at)})</>}.</Notice>}
            </SectionCard>
          </div>
          <SectionCard icon="clock" title="Recent activity">
            <Q q={deps} fallback="text">{(v) => v.items.length ? <DeploymentHistory items={v.items.slice(0, 5)} /> : <EmptyState icon="deploy" hint="Deploy a revision to see it here.">No deployments yet.</EmptyState>}</Q>
          </SectionCard>
        </>}
        {tab === 'revisions' && <SectionCard icon="document" title="Revisions">
          <Q q={revs} fallback="text">{(v) => v.items.length ? <>
            <ul className="list">{v.items.slice((revPage - 1) * REV_PAGE, revPage * REV_PAGE).map((x) =>
              <li key={x.number} className="list-row items-center px-0"><span className="grid place-items-center size-9 rounded-box bg-primary/10 text-primary"><Icon name="document" className="size-5" /></span>
                <div className="list-col-grow"><Link to={`/n/${ns}/s/${stack}/revisions/${x.number}`} className="font-medium">Revision {x.number}</Link></div><ProfileBadge profile={x.safety_profile} /></li>)}</ul>
            <Pager page={revPage} pages={Math.ceil(v.items.length / REV_PAGE)} onPage={setRevPage} /></>
            : <EmptyState icon="document" to={`/n/${ns}/s/${stack}/revisions/new`} cta="Create the first revision" hint="A revision is a saved, policy-checked set of manifests.">No revisions yet.</EmptyState>}</Q>
        </SectionCard>}
        {tab === 'deployments' && <SectionCard flush icon="deploy" title="Recent deployments">
          <div className="px-5 pb-5"><Q q={deps} fallback="table">{(v) => v.items.length ? <DeploymentRows items={v.items.slice(0, 10)} /> : <EmptyState icon="deploy">No deployments yet.</EmptyState>}</Q></div>
        </SectionCard>}
        {tab === 'audit' && <SectionCard icon="list" title="Audit"><AuditFeed namespace={ns} /></SectionCard>}
        {tab === 'settings' && <StackActions s={s} />}
      </Tabs>
    </Page>}</Q>
  )
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
  const sect = (title: string, icon: 'shield' | 'cube' | 'document' | 'drift', children: ReactNode, open = false) =>
    <SectionCard collapsible defaultOpen={open} icon={icon} title={title}>{children}</SectionCard>
  return (
    <Q q={q} fallback="page">{(r) => <Page>
      <PageHeader icon="document" title={`Revision ${r.number}`} subtitle={<>of <Link to={`/n/${ns}/s/${stack}`}>{stack}</Link> {'·'} created {when(r.created_at)} by {r.created_by}</>}
        badges={<>{st.data && <TierBadge tier={st.data.environment_tier} large />}<ProfileBadge profile={r.safety_profile} large /><span className="badge badge-lg badge-outline">origin {r.origin}</span></>}
        actions={<>
          <Link className="btn btn-sm" to={`/n/${ns}/s/${stack}`}><Icon name="arrow-left" className="size-4" />Back to {stack}</Link>
          {st.data && (access.allowed ? <button className="btn btn-primary btn-sm" onClick={() => setDeploying(true)}><Icon name="deploy" className="size-4" />Deploy revision {r.number}</button>
            : <Tip tip="Prod needs an active access grant. Request one first." pos="left"><button className="btn btn-primary btn-sm" disabled>Deploy revision {r.number}</button></Tip>)}</>} />
      {st.data && <>
        <TierBanner tier={st.data.environment_tier} />
        <GrantState s={st.data} />
        {deploying && <DeployDialog s={st.data} r={r} onClose={() => setDeploying(false)} />}</>}
      <div className="flex flex-col gap-3">
        {sect('Policy result', 'shield', <>
          {r.policy_result?.violations?.length ? <div className="flex flex-col gap-2">{r.policy_result.violations.map((v, i) => <Notice key={i} tone="error" title={v.rule}>{v.message}</Notice>)}</div>
            : <Notice tone={r.policy_result?.allowed === false ? 'error' : 'success'}>{r.policy_result?.allowed === false ? 'Not allowed.' : 'No violations.'}</Notice>}
          {r.policy_result?.approval_reasons?.length ? <Notice tone="warning">Needs approval: {r.policy_result.approval_reasons.join('; ')}</Notice> : null}</>, true)}
        {sect('Objects', 'cube', r.inventory?.length ? <ul className="flex flex-wrap gap-2">{r.inventory.map((o, i) => <li key={i} className="badge badge-lg badge-ghost font-mono">{o.kind}/{o.name}</li>)}</ul> : <p>None.</p>)}
        {sect('Manifests', 'document', <MockCode text={text(r)} label="Manifests" />, true)}
        {base != null && sect(`Changes since revision ${base}`, 'drift', <Q q={prev} fallback="text">{(p) => <DiffView a={text(p)} b={text(r)} an={base} bn={r.number!} />}</Q>)}
      </div>
    </Page>}</Q>
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
      const mainSteps = ['Queued', 'Running', last].map((label, i): { label: string; state: StepState } => ({ label,
        state: i > at ? 'todo' : i === 2 ? (d.status === 'failed' ? 'error' : 'success') : i === at ? 'current' : 'done' }))
      const events = ([[`Requested by ${d.requested_by}${d.reason ? `: ${d.reason}` : ''}`, 'primary'], [d.approved_by && `Approved by ${d.approved_by}`, 'success'], [d.started_at && `Started ${when(d.started_at)}`, 'primary'],
        [d.finished_at && `Finished ${when(d.finished_at)} — ${d.status}`, d.status === 'succeeded' ? 'success' : 'error']] as [string | null | undefined, Accent][]).filter(([t]) => t) as [string, Accent][]
      return <Page>
        <PageHeader icon="deploy" title={`Deployment ${d.id}`} subtitle={<>{d.namespace && <><Link to={`/n/${d.namespace}`}>{d.namespace}</Link>{d.stack && <>/<Link to={`/n/${d.namespace}/s/${d.stack}`}>{d.stack}</Link></>} {'·'} </>}{d.action}{d.revision != null && <> of revision {d.revision}</>}</>}
          badges={<><TierBadge tier={d.environment_tier} large /><ProfileBadge profile={d.safety_profile} large /><StatusBadge text={d.status} />{d.grant_id && <span className="badge badge-outline">grant {d.grant_id}</span>}</>}
          actions={<DeploymentActions d={d} />} />
        <TierBanner tier={d.environment_tier} />
        {d.grant_bypass && <Notice tone="warning">Ran with grant bypassed (break-glass session).</Notice>}
        {d.error && <Notice tone="error" role="alert" title="Deployment failed">Error: {d.error}</Notice>}
        {roll && <Notice tone="warning" title={d.rollout?.state === 'rolling_back' ? 'Rolling back' : 'Rolled back'}>{d.rollout?.state === 'rolling_back' ? `Rolling back to revision ${d.rollout.rolled_back_to ?? '?'}` : `Rollout rolled back to revision ${d.rollout?.rolled_back_to ?? '?'}`}</Notice>}
        <SectionCard icon="bolt" title="Progress">
          <Stepper label="Deployment state" steps={mainSteps} />
          {at < 0 && <p className="text-sm">This deployment is {d.status?.replace('_', ' ')}.</p>}
          {roll && <Stepper label="Rollback state" steps={[{ label: 'Rollout failed', state: 'error' }, { label: 'Rolling back', state: d.rollout?.state === 'rolling_back' ? 'warning' : 'done' },
            { label: d.rollout?.result === 'rollback_failed' ? 'Rollback failed' : 'Restored', state: d.rollout?.result === 'rolled_back' ? 'success' : d.rollout?.result === 'rollback_failed' ? 'error' : 'todo' }]} />}
          {d.rollout?.result && <p>Rollout result: {d.rollout.result}</p>}
        </SectionCard>
        <div className="grid gap-5 lg:grid-cols-3">
          <SectionCard icon="clock" title="Timeline" className="lg:col-span-1"><Timeline label="Deployment timeline" items={events.map(([t, tone], i) => ({ id: i, tone, content: t }))} /></SectionCard>
          <SectionCard flush icon="refresh" title="Rollout" className="lg:col-span-2">
            <div className="px-5 pb-5">{d.rollout?.workloads?.length ? <DataTable label="Rollout" rows={d.rollout.workloads} rowKey={(w) => `${w.kind}/${w.name}`} columns={[
              { key: 'w', header: 'Workload', rowHeader: true, cell: (w) => <>{w.kind}/{w.name}</> }, { key: 'd', header: 'Desired', cell: (w) => w.desired }, { key: 'u', header: 'Updated', cell: (w) => w.updated },
              { key: 'r', header: 'Ready', cell: (w) => <>{w.ready} <progress className="progress progress-primary w-16 align-middle" value={w.ready ?? 0} max={Math.max(w.desired ?? 0, 1)} aria-hidden="true" /></> },
              { key: 'a', header: 'Available', hideBelow: 'sm', cell: (w) => w.available }, { key: 'x', header: 'Reason', hideBelow: 'md', cell: (w) => w.reason ?? '' }]} />
              : <EmptyState icon="refresh">No rollout data.</EmptyState>}</div>
          </SectionCard>
        </div>
        <SectionCard collapsible defaultOpen icon="document" title="Output"><MockCode text={out.data?.output ?? ''} label="Deployment output" prefix="$" /></SectionCard>
      </Page>
    }}</Q>
  )
}
