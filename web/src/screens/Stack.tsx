import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router'
import { ProfileBadge, TierBadge } from '../components/Badges'
import { TargetNote } from '../components/Rows'
import { get, hhmm, profileTag, Q, remaining, sumReady, Table, useFlash, useGet, useMe, useNow, useTitle, when, type S } from '../lib'

const Workload = ({ s }: { s: S['Service'] }) => {
  const n = useFlash(JSON.stringify([s.ready_replicas, s.desired_replicas, s.image_ref, s.state]))
  return (
    <tr key={n} className={n ? 'flash' : ''}>
      <th scope="row">{s.kind}/{s.name}</th>
      <td>{s.ready_replicas ?? 0}/{s.desired_replicas ?? 0}</td>
      <td>{s.image_ref ?? '—'}</td>
      <td>{s.image_id ?? '—'}</td>
    </tr>
  )
}

export const DeploymentRows = ({ items }: { items: S['Deployment'][] }) => (
  <Table label="Deployments" head={['Deployment', 'Revision', 'Safety profile', 'Status', 'Requested by']}>
    {items.map((d) => <tr key={d.id}>
      <th scope="row"><Link to={`/deployments/${d.id}`}>{d.id}</Link></th>
      <td>{d.revision ?? '—'}</td><td><ProfileBadge profile={d.safety_profile} /></td><td>{d.status}</td><td>{d.requested_by}</td></tr>)}
  </Table>
)

// UI-016 (read side): grant state for a prod stack.
function GrantState({ s }: { s: S['Stack'] }) {
  const me = useMe().data!
  const now = useNow()
  const grants = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  if (s.environment_tier !== 'prod') return null
  if (me.auth_method === 'break_glass') return <p className="badge none">Grant bypassed (break-glass)</p>
  const mine = (grants.data?.items ?? []).filter((g) => g.requester === me.id && g.namespace === s.namespace && g.target === s.target)
  const active = mine.find((g) => g.status === 'active' && remaining(g.expires_at, now))
  if (active) return <p>Access until {hhmm(active.expires_at)} {'—'} {remaining(active.expires_at, now)}</p>
  if (mine.some((g) => g.status === 'requested')) return <p>Awaiting approval</p>
  return <p><Link to={`/access/request?namespace=${s.namespace}&target=${s.target}`}>Request prod access</Link></p>
}

export function Stack() {
  const { ns = '', stack = '' } = useParams()
  const q = useGet<S['Stack']>(['stack', ns, stack], `/namespaces/${ns}/stacks/${stack}`, { path: { ns, stack } })
  const deps = useGet<{ items: S['Deployment'][] }>(['deployments', ns, stack], `/namespaces/${ns}/stacks/${stack}/deployments`, { path: { ns, stack } })
  const revs = useGet<{ items: S['Revision'][] }>(['revisions', ns, stack], `/namespaces/${ns}/stacks/${stack}/revisions`, { path: { ns, stack } })
  const s = q.data
  // UI-012
  useTitle(s ? `${stack} — ${ns} [${s.environment_tier === 'prod' ? 'PROD' : 'NON-PROD'}] ${profileTag(s.safety_profile)}`.trim() : stack)
  const running = (deps.data?.items ?? []).find((d) => d.status === 'running' && d.rollout?.workloads?.length)
  return (
    <Q q={q}>{(s) => { const [r, d] = sumReady(s); return <>
      <h1>{s.slug}</h1>
      <p><TierBadge tier={s.environment_tier} /> <ProfileBadge profile={s.safety_profile} large /> Health: <strong>{s.health ?? 'unknown'}</strong> {'·'} {r}/{d} ready {'·'} target {s.target}</p>
      <TargetNote target={s.target} />
      {s.rollout_frozen && <div className="banner-red" role="alert">Automatic updates stopped after a failed rollback {'—'} an admin must deploy a revision</div>}
      <GrantState s={s} />
      <h2>Workloads</h2>
      {s.services?.length ? <Table label="Workloads" head={['Workload', 'Ready', 'Image', 'Digest']}>{s.services.map((v) => <Workload key={`${v.kind}/${v.name}`} s={v} />)}</Table> : <p>No workloads observed.</p>}
      {running && <section aria-labelledby="h-roll"><h2 id="h-roll">Rollout in progress</h2>
        {running.rollout!.workloads!.map((w) => <p key={`${w.kind}/${w.name}`}>
          <label>{w.kind}/{w.name} {w.ready ?? 0}/{w.desired ?? 0} <progress value={w.ready ?? 0} max={Math.max(w.desired ?? 0, 1)} /></label></p>)}</section>}
      <h2>Drift</h2>
      {s.drift?.length ? <ul>{s.drift.map((x, i) => <li key={i}>{x.kind}: {x.message}</li>)}</ul> : <p>No drift.</p>}
      <h2>Digest updates available</h2>
      {s.digest_status?.updates?.length ? <ul>{s.digest_status.updates.map((u, i) => <li key={i}>{u.workload}/{u.container}: {u.current ?? 'unknown'} {'→'} {u.available}</li>)}</ul> : <p>None{s.digest_status?.checked_at && <> (checked {when(s.digest_status.checked_at)})</>}.</p>}
      <h2>Revisions</h2>
      <Q q={revs}>{(v) => v.items.length ? <ul>{v.items.map((x) => <li key={x.number}><Link to={`/n/${ns}/s/${stack}/revisions/${x.number}`}>Revision {x.number}</Link> <ProfileBadge profile={x.safety_profile} /></li>)}</ul> : <p>No revisions yet.</p>}</Q>
      <h2>Recent deployments</h2>
      <Q q={deps}>{(v) => v.items.length ? <DeploymentRows items={v.items.slice(0, 10)} /> : <p>No deployments yet.</p>}</Q>
    </> }}</Q>
  )
}

export function Revision() {
  const { ns = '', stack = '', n = '' } = useParams()
  const q = useGet<S['Revision']>(['revisions', ns, stack, n], `/namespaces/${ns}/stacks/${stack}/revisions/${n}`, { path: { ns, stack, n: Number(n) } })
  useTitle(`Revision ${n} — ${stack}`)
  return (
    <Q q={q}>{(r) => <>
      <h1>Revision {r.number}</h1>
      <p><ProfileBadge profile={r.safety_profile} large /> origin {r.origin} {'·'} created {when(r.created_at)} by {r.created_by}</p>
      <p><Link to={`/n/${ns}/s/${stack}`}>Back to {stack}</Link></p>
      <h2>Policy result</h2>
      {r.policy_result?.violations?.length ? <ul>{r.policy_result.violations.map((v, i) => <li key={i}>{v.rule}: {v.message}</li>)}</ul> : <p>{r.policy_result?.allowed === false ? 'Not allowed.' : 'No violations.'}</p>}
      {r.policy_result?.approval_reasons?.length ? <p>Needs approval: {r.policy_result.approval_reasons.join('; ')}</p> : null}
      <h2>Objects</h2>
      {r.inventory?.length ? <ul>{r.inventory.map((o, i) => <li key={i}>{o.kind}/{o.name}</li>)}</ul> : <p>None.</p>}
      <h2>Manifests</h2>
      <pre tabIndex={0} className="scroll">{r.manifests_input ?? r.compose_input ?? ''}</pre>
    </>}</Q>
  )
}

// UI-011/018: timeline, per-workload rollout table; output is refetched whenever the status changes.
export function Deployment() {
  const { id = '' } = useParams()
  const q = useGet<S['Deployment']>(['deployment', id], `/deployments/${id}`, { path: { id } })
  const status = q.data?.status
  const out = useQuery({ queryKey: ['deployment-output', id, status], enabled: !!status, queryFn: () => get<S['Deployment']>(`/deployments/${id}`, { path: { id } }) })
  useTitle(`Deployment ${id}`)
  return (
    <Q q={q}>{(d) => <>
      <h1>Deployment {d.id}</h1>
      <p><ProfileBadge profile={d.safety_profile} large /> <TierBadge tier={d.environment_tier} /> Status: <strong>{d.status}</strong>
        {d.grant_bypass && <> {'·'} grant bypassed (break-glass)</>}{d.grant_id && <> {'·'} grant {d.grant_id}</>}</p>
      {d.rollout?.state === 'rolling_back' && <p role="status">Rolling back to revision {d.rollout.rolled_back_to ?? '?'}</p>}
      {d.rollout?.result && <p>Rollout result: {d.rollout.result}</p>}
      <h2>Timeline</h2>
      <ol>
        <li>Requested by {d.requested_by}{d.reason && <>: {d.reason}</>}</li>
        {d.approved_by && <li>Approved by {d.approved_by}</li>}
        {d.started_at && <li>Started {when(d.started_at)}</li>}
        {d.finished_at && <li>Finished {when(d.finished_at)} {'—'} {d.status}</li>}
      </ol>
      {d.error && <p role="alert">Error: {d.error}</p>}
      <h2>Rollout</h2>
      {d.rollout?.workloads?.length ? <Table label="Rollout" head={['Workload', 'Desired', 'Updated', 'Ready', 'Available', 'Reason']}>
        {d.rollout.workloads.map((w) => <tr key={`${w.kind}/${w.name}`}><th scope="row">{w.kind}/{w.name}</th><td>{w.desired}</td><td>{w.updated}</td><td>{w.ready}</td><td>{w.available}</td><td>{w.reason ?? ''}</td></tr>)}</Table> : <p>No rollout data.</p>}
      <h2>Output</h2>
      <pre tabIndex={0} className="scroll">{out.data?.output ?? ''}</pre>
    </>}</Q>
  )
}
