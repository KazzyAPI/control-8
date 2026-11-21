import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { StackRow, STACK_HEAD } from '../components/Rows'
import { TierBadge } from '../components/Badges'
import { Countdown, Empty, PageHeader, ProdRibbon, Radial, Stat, Stats, Tabs } from '../components/Parts'
import { CreateAsset, CreateStack, ApproverGroup } from './Write'
import { AuditFeed } from './Feeds'
import { Q, Table, useGet, useMe, useNow, remaining, hhmm, useTitle, type S } from '../lib'

const useInventory = () => useGet<{ items: S['Stack'][] }>(['inventory'], '/inventory')
const useGrants = () => useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')

// UI-016: countdown of the viewer's own active grants (text, countdown, and a radial of the time left).
export function GrantList({ grants, empty }: { grants: S['AccessGrant'][]; empty: string }) {
  const now = useNow()
  const live = grants.filter((g) => g.status === 'active' && remaining(g.expires_at, now))
  if (!live.length) return <Empty to="/access/request" cta="Request prod access">{empty}</Empty>
  return <ul className="list bg-base-100 rounded-box border border-base-300">{live.map((g) => {
    const left = Date.parse(g.expires_at!) - now
    const total = (g.duration_minutes ?? 0) * 60000
    return <li key={g.id} className="list-row items-center">
      <Radial value={total ? (left / total) * 100 : 100} label={`Time left on grant for ${g.namespace} on ${g.target}`} />
      <div>{g.namespace} on {g.target}: access until {hhmm(g.expires_at)} {'—'} {remaining(g.expires_at, now)}</div>
      <Countdown ms={left} />
    </li>
  })}</ul>
}

// UI-010..015: stacks grouped by namespace, target cards, pending approvals, my grants.
export function Overview() {
  useTitle('Overview')
  const me = useMe().data!
  const inv = useInventory()
  const nss = useGet<{ items: S['Namespace'][] }>(['namespaces'], '/namespaces')
  const targets = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  const grants = useGrants()
  const pending = (grants.data?.items ?? []).filter((g) => g.status === 'requested' && (me.global_admin || me.approver_for?.includes(g.namespace ?? ''))).length
  const groups = new Map<string, S['Stack'][]>((nss.data?.items ?? []).map((n) => [n.slug!, []]))
  for (const s of inv.data?.items ?? []) groups.set(s.namespace!, [...(groups.get(s.namespace!) ?? []), s])
  return (
    <>
      <PageHeader title="Overview" />
      <Q q={inv} fallback="stats">{(d) => {
        const bad = d.items.filter((s) => s.health === 'degraded' || s.health === 'drifted' || (s.drift?.length ?? 0) > 0).length
        return <Stats>
          <Stat title="Stacks" value={d.items.length} desc={`in ${groups.size} namespace${groups.size === 1 ? '' : 's'}`} />
          <Stat title="Healthy" value={d.items.filter((s) => s.health === 'healthy').length} desc="reporting healthy" />
          <Stat title="Need attention" value={bad} desc="degraded or drifted" />
          <Stat title="Prod stacks" value={d.items.filter((s) => s.environment_tier === 'prod').length} desc="changes need a grant" />
        </Stats>
      }}</Q>
      <section aria-labelledby="h-targets"><h2 id="h-targets">Targets</h2>
        <Q q={targets} fallback="cards">{(d) => d.items.length ? <ul className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">{d.items.map((t) =>
          <li key={t.name} className="card card-border bg-base-100"><div className="card-body p-4 gap-1">
            <strong className="card-title">{t.name} <TierBadge tier={t.environment_tier} /></strong>
            <span className="flex items-center gap-2"><span className={`status ${t.reachable ? 'status-success' : 'status-error'}`} aria-hidden="true" />{t.reachable ? 'Reachable' : 'Unreachable'} {'·'} {t.sync ?? 'unknown'} {'·'} last seen {hhmm(t.last_seen_at)}</span>
          </div></li>)}</ul> : <Empty>No targets configured.</Empty>}</Q>
      </section>
      <section aria-labelledby="h-pending"><h2 id="h-pending">Pending approvals</h2>
        <Stats><Stat title="Waiting" value={pending} desc={<>{pending} access request{pending === 1 ? '' : 's'} waiting {pending > 0 && <Link to="/access/approvals">Open queue</Link>}</>} /></Stats></section>
      <section aria-labelledby="h-mine"><h2 id="h-mine">My active grants</h2><GrantList grants={me.active_grants ?? []} empty="No active prod access." /></section>
      <Q q={inv} fallback="table">{() => <>{[...groups].map(([ns, stacks]) =>
        <section key={ns} aria-labelledby={`h-${ns}`}><h2 id={`h-${ns}`}><Link to={`/n/${ns}`}>{ns}</Link></h2>
          {stacks.length ? <Table label={`Stacks in ${ns}`} head={STACK_HEAD}>{stacks.map((s) => <StackRow key={s.id} s={s} />)}</Table> : <Empty to={`/n/${ns}`} cta="Create a stack">No stacks.</Empty>}
        </section>)}</>}</Q>
    </>
  )
}

const stacksIn = (inv: ReturnType<typeof useInventory>, keep: (s: S['Stack']) => boolean) =>
  <Q q={inv} fallback="table">{(i) => { const st = i.items.filter(keep)
    return st.length ? <Table label="Stacks" head={STACK_HEAD}>{st.map((s) => <StackRow key={s.id} s={s} />)}</Table> : <Empty>No stacks.</Empty> }}</Q>

export function Namespace() {
  const { ns = '' } = useParams()
  const me = useMe().data!
  const [tab, setTab] = useState<'overview' | 'audit' | 'settings'>('overview')
  useTitle(ns)
  const n = useGet<S['Namespace']>(['namespace', ns], `/namespaces/${ns}`, { path: { ns } })
  const assets = useGet<{ items: S['Asset'][] }>(['assets', ns], `/namespaces/${ns}/assets`, { path: { ns } })
  const pol = useGet<S['PolicyRules']>(['policy', ns], `/namespaces/${ns}/policy/effective`, { path: { ns } })
  const inv = useInventory()
  return (
    <Q q={n} fallback="page">{(d) => <>
      <ProdRibbon tier={d.environment_tier} />
      <PageHeader title={d.display_name || ns} badges={d.environment_tier && <TierBadge tier={d.environment_tier} large />} />
      <p>Approver group: {d.approver_group ?? 'none'}</p>
      {d.description && <p>{d.description}</p>}
      <Tabs label="Namespace sections" value={tab} onChange={setTab} tabs={[['overview', 'Overview'], ['audit', 'Audit'], ['settings', 'Settings']]}>
        {tab === 'overview' && <>
          <h2 className="mt-0">Assets</h2>
          <Q q={assets}>{(a) => a.items.length ? <ul className="list bg-base-100 rounded-box border border-base-300">{a.items.map((x) =>
            <li key={x.slug} className="list-row items-center"><div><Link to={`/n/${ns}/assets/${x.slug}`}>{x.name}</Link></div>
              <div className="text-sm">{x.managed_by}{x.image_repo && <> {'·'} {x.image_repo}</>}</div></li>)}</ul> : <Empty>No assets.</Empty>}</Q>
          <h2>Stacks</h2>
          {stacksIn(inv, (s) => s.namespace === ns)}
        </>}
        {tab === 'audit' && <AuditFeed namespace={ns} />}
        {tab === 'settings' && <>
          {me.global_admin && <ApproverGroup ns={ns} current={d.approver_group} />}
          <div className="grid gap-6 lg:grid-cols-2"><CreateAsset ns={ns} /><CreateStack ns={ns} /></div>
          <h2>Policy summary</h2>
          <div className="collapse collapse-arrow bg-base-200"><input type="checkbox" aria-label="Show effective policy" defaultChecked />
            <div className="collapse-title font-semibold">Effective policy</div>
            <div className="collapse-content"><Q q={pol}>{(p) => <ul className="list">
              <li className="list-row">Allowed registries: {(p.allowed_registries ?? []).join(', ') || 'any'}</li>
              <li className="list-row">Allowed safety profiles: {(p.allowed_safety_profiles ?? []).join(', ') || 'any'}</li>
              <li className="list-row">Required labels: {(p.required_labels ?? []).join(', ') || 'none'}</li>
              <li className="list-row">Privileged containers: {p.allow_privileged ? 'allowed' : 'denied'}</li></ul>}</Q></div></div>
        </>}
      </Tabs>
    </>}</Q>
  )
}

export function Asset() {
  const { ns = '', asset = '' } = useParams()
  useTitle(`${asset} — ${ns}`)
  const a = useGet<S['Asset']>(['asset', ns, asset], `/namespaces/${ns}/assets/${asset}`, { path: { ns, asset } })
  const inv = useInventory()
  return (
    <Q q={a} fallback="page">{(d) => <>
      <PageHeader title={d.name} badges={<span className="badge badge-outline badge-lg">{d.managed_by}</span>} />
      <p>Managed by {d.managed_by}{d.owner && <> {'·'} owner {d.owner}</>}{d.image_repo && <> {'·'} {d.image_repo}</>}</p>
      {d.description && <p>{d.description}</p>}
      <h2>Stacks</h2>
      {stacksIn(inv, (s) => s.namespace === ns && s.asset === asset)}
    </>}</Q>
  )
}
