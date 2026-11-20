import { Link, useParams } from 'react-router'
import { StackRow, STACK_HEAD } from '../components/Rows'
import { TierBadge } from '../components/Badges'
import { Q, Table, useGet, useMe, useNow, remaining, hhmm, useTitle, type S } from '../lib'

const useInventory = () => useGet<{ items: S['Stack'][] }>(['inventory'], '/inventory')
const useGrants = () => useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')

// UI-016: countdown of the viewer's own active grants.
export function GrantList({ grants, empty }: { grants: S['AccessGrant'][]; empty: string }) {
  const now = useNow()
  const live = grants.filter((g) => g.status === 'active' && remaining(g.expires_at, now))
  if (!live.length) return <p>{empty}</p>
  return <ul>{live.map((g) => <li key={g.id}>{g.namespace} on {g.target}: access until {hhmm(g.expires_at)} {'—'} {remaining(g.expires_at, now)}</li>)}</ul>
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
      <h1>Overview</h1>
      <section aria-labelledby="h-targets"><h2 id="h-targets">Targets</h2>
        <Q q={targets}>{(d) => <ul className="cards">{d.items.map((t) =>
          <li key={t.name}><strong>{t.name}</strong> <TierBadge tier={t.environment_tier} /><br />{t.reachable ? 'Reachable' : 'Unreachable'} {'·'} {t.sync ?? 'unknown'} {'·'} last seen {hhmm(t.last_seen_at)}</li>)}</ul>}</Q>
      </section>
      <section aria-labelledby="h-pending"><h2 id="h-pending">Pending approvals</h2>
        <p>{pending} access request{pending === 1 ? '' : 's'} waiting</p></section>
      <section aria-labelledby="h-mine"><h2 id="h-mine">My active grants</h2><GrantList grants={me.active_grants ?? []} empty="No active prod access." /></section>
      <Q q={inv}>{() => <>{[...groups].map(([ns, stacks]) =>
        <section key={ns} aria-labelledby={`h-${ns}`}><h2 id={`h-${ns}`}><Link to={`/n/${ns}`}>{ns}</Link></h2>
          {stacks.length ? <Table label={`Stacks in ${ns}`} head={STACK_HEAD}>{stacks.map((s) => <StackRow key={s.id} s={s} />)}</Table> : <p>No stacks.</p>}
        </section>)}</>}</Q>
    </>
  )
}

export function Namespace() {
  const { ns = '' } = useParams()
  useTitle(ns)
  const n = useGet<S['Namespace']>(['namespace', ns], `/namespaces/${ns}`, { path: { ns } })
  const assets = useGet<{ items: S['Asset'][] }>(['assets', ns], `/namespaces/${ns}/assets`, { path: { ns } })
  const pol = useGet<S['PolicyRules']>(['policy', ns], `/namespaces/${ns}/policy/effective`, { path: { ns } })
  const inv = useInventory()
  return (
    <Q q={n}>{(d) => <>
      <h1>{d.display_name || ns}</h1>
      <p>{d.environment_tier && <TierBadge tier={d.environment_tier} />} Approver group: {d.approver_group ?? 'none'}</p>
      {d.description && <p>{d.description}</p>}
      <h2>Assets</h2>
      <Q q={assets}>{(a) => a.items.length ? <ul>{a.items.map((x) => <li key={x.slug}><Link to={`/n/${ns}/assets/${x.slug}`}>{x.name}</Link></li>)}</ul> : <p>No assets.</p>}</Q>
      <h2>Stacks</h2>
      <Q q={inv}>{(i) => { const st = i.items.filter((s) => s.namespace === ns)
        return st.length ? <Table label="Stacks" head={STACK_HEAD}>{st.map((s) => <StackRow key={s.id} s={s} />)}</Table> : <p>No stacks.</p> }}</Q>
      <h2>Policy summary</h2>
      <Q q={pol}>{(p) => <ul>
        <li>Allowed registries: {(p.allowed_registries ?? []).join(', ') || 'any'}</li>
        <li>Allowed safety profiles: {(p.allowed_safety_profiles ?? []).join(', ') || 'any'}</li>
        <li>Required labels: {(p.required_labels ?? []).join(', ') || 'none'}</li>
        <li>Privileged containers: {p.allow_privileged ? 'allowed' : 'denied'}</li></ul>}</Q>
    </>}</Q>
  )
}

export function Asset() {
  const { ns = '', asset = '' } = useParams()
  useTitle(`${asset} — ${ns}`)
  const a = useGet<S['Asset']>(['asset', ns, asset], `/namespaces/${ns}/assets/${asset}`, { path: { ns, asset } })
  const inv = useInventory()
  return (
    <Q q={a}>{(d) => <>
      <h1>{d.name}</h1>
      <p>Managed by {d.managed_by}{d.owner && <> {'·'} owner {d.owner}</>}{d.image_repo && <> {'·'} {d.image_repo}</>}</p>
      {d.description && <p>{d.description}</p>}
      <h2>Stacks</h2>
      <Q q={inv}>{(i) => { const st = i.items.filter((s) => s.namespace === ns && s.asset === asset)
        return st.length ? <Table label="Stacks" head={STACK_HEAD}>{st.map((s) => <StackRow key={s.id} s={s} />)}</Table> : <p>No stacks.</p> }}</Q>
    </>}</Q>
  )
}
