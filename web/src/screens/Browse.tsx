import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { healthOf, NoStacks, StackTable } from '../components/Rows'
import { Avatar, Countdown, EmptyState, Icon, KeyValueList, Notice, Page, PageHeader, Radial, SectionCard, StatCard, StatGrid, StatusBadge, Tabs, TierBadge, TierBanner, Tip } from '../components/ui'
import { CreateAsset, CreateStack, ApproverGroup } from './Write'
import { AuditFeed } from './Feeds'
import { QuickDecision, RevokeButton } from './Access'
import { Q, remaining, sumReady, hhmm, useGet, useMe, useNow, useTitle, type S } from '../lib'

const useInventory = () => useGet<{ items: S['Stack'][] }>(['inventory'], '/inventory')
const useGrants = () => useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
const bad = (s: S['Stack']) => s.health === 'degraded' || s.health === 'drifted' || (s.drift?.length ?? 0) > 0

// UI-016: the viewer's own active grants as cards: radial of the time left, the countdown, and the plain-text reading.
export function GrantCards({ grants, empty }: { grants: S['AccessGrant'][]; empty: string }) {
  const now = useNow()
  const live = grants.filter((g) => g.status === 'active' && remaining(g.expires_at, now))
  if (!live.length) return <EmptyState icon="key" to="/access/request" cta="Request prod access" hint="Prod changes need an approved, time-boxed grant.">{empty}</EmptyState>
  return <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-1 2xl:grid-cols-2">{live.map((g) => {
    const left = Date.parse(g.expires_at!) - now
    const total = (g.duration_minutes ?? 0) * 60000
    const pct = total ? (left / total) * 100 : 100
    return <li key={g.id} className="card card-border bg-base-100 border-t-4 border-t-success"><div className="card-body p-4 gap-3">
      <div className="flex items-center gap-3">
        <Radial value={pct} tone={pct < 15 ? 'error' : pct < 40 ? 'warning' : 'success'} label={`Time left on grant for ${g.namespace} on ${g.target}`} />
        <div className="min-w-0"><p className="font-semibold break-words">{g.namespace} on {g.target}</p>
          <p className="text-sm">Access until {hhmm(g.expires_at)} {'—'} {remaining(g.expires_at, now)}</p></div>
      </div>
      <div className="flex items-center justify-between gap-2"><Countdown ms={left} className="text-2xl" /><RevokeButton g={g} /></div>
    </div></li>
  })}</ul>
}

// Targets as cards: reachability dot, tier, version, and the ready ratio of the stacks that run on it.
function TargetCards({ targets, stacks }: { targets: S['Target'][]; stacks: S['Stack'][] }) {
  return <ul className="grid gap-3 sm:grid-cols-2">{targets.map((t) => {
    const on = stacks.filter((s) => s.target === t.name)
    const [r, d] = on.reduce((a, s) => { const [x, y] = sumReady(s); return [a[0] + x, a[1] + y] }, [0, 0])
    return <li key={t.name} className="card card-border bg-base-100"><div className="card-body p-4 gap-3">
      <div className="flex items-start gap-3">
        <span className="grid place-items-center size-10 rounded-box bg-primary/10 text-primary"><Icon name="server" /></span>
        <div className="min-w-0 flex-1"><p className="card-title text-base break-words">{t.name}</p>
          <p className="text-sm">{t.kind}{t.server_version && <> {'·'} {t.server_version}</>}</p></div>
        <TierBadge tier={t.environment_tier} />
      </div>
      <p className="flex items-center gap-2 text-sm"><span className={`status ${t.reachable ? 'status-success' : 'status-error'}`} aria-hidden="true" />{t.reachable ? 'Reachable' : 'Unreachable'} {'·'} {t.sync ?? 'unknown'} {'·'} last seen {hhmm(t.last_seen_at)}</p>
      <div className="text-sm"><div className="flex justify-between"><span>{on.length} stack{on.length === 1 ? '' : 's'}</span><span>{d ? `${r}/${d} ready` : 'no workloads'}</span></div>
        <progress className={`progress w-full ${r < d ? 'progress-warning' : 'progress-success'}`} value={r} max={Math.max(d, 1)} aria-label={`Ready replicas on ${t.name}`} /></div>
    </div></li>
  })}</ul>
}

// UI-010..015: stat tiles, what needs attention, grants, targets, pending approvals, then stacks grouped by namespace.
export function Overview() {
  useTitle('Overview')
  const me = useMe().data!
  const inv = useInventory()
  const nss = useGet<{ items: S['Namespace'][] }>(['namespaces'], '/namespaces')
  const targets = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  const grants = useGrants()
  const pending = (grants.data?.items ?? []).filter((g) => g.status === 'requested' && (me.global_admin || me.approver_for?.includes(g.namespace ?? '')))
  const groups = new Map<string, S['Stack'][]>((nss.data?.items ?? []).map((n) => [n.slug!, []]))
  for (const s of inv.data?.items ?? []) groups.set(s.namespace!, [...(groups.get(s.namespace!) ?? []), s])
  const items = inv.data?.items ?? []
  const down = (targets.data?.items ?? []).filter((t) => t.reachable === false)
  const issues = items.filter(bad)
  return (
    <Page>
      <PageHeader icon="home" title="Overview" subtitle="Stacks, targets and prod access across every namespace."
        actions={<Link className="btn btn-primary btn-sm" to="/access/request"><Icon name="key" className="size-4" />Request prod access</Link>} />
      <Q q={inv} fallback="stats">{(d) => {
        const healthy = d.items.filter((s) => s.health === 'healthy').length
        return <StatGrid>
          <StatCard icon="layers" accent="primary" title="Stacks" value={d.items.length} desc={`in ${groups.size} namespace${groups.size === 1 ? '' : 's'}`} />
          <StatCard accent="success" title="Healthy" value={healthy} desc="reporting healthy" progress={d.items.length ? (healthy / d.items.length) * 100 : 0} />
          <StatCard icon="warning" accent={issues.length ? 'warning' : 'success'} title="Need attention" value={issues.length} desc="degraded or drifted" />
          <StatCard icon="lock" accent="error" title="Prod stacks" value={d.items.filter((s) => s.environment_tier === 'prod').length} desc="changes need a grant" />
        </StatGrid>
      }}</Q>
      <div className="grid items-start gap-5 lg:grid-cols-3">
        <SectionCard className="lg:col-span-2" icon="bell" title="Needs attention" actions={<span className="badge badge-primary">{issues.length + down.length + (pending.length ? 1 : 0)}</span>}>
          <div className="flex flex-col gap-2">
            {issues.map((s) => { const [r, d] = sumReady(s)
              return <Notice key={s.id} tone="warning" action={<Link className="btn btn-sm" to={`/n/${s.namespace}/s/${s.slug}`}>Open stack</Link>}>
                <strong>{s.namespace}/{s.slug}</strong> is {healthOf(s)}{d > 0 && <> {'—'} {r}/{d} ready</>}{(s.drift?.length ?? 0) > 0 && <> {'·'} {s.drift!.length} drift item{s.drift!.length === 1 ? '' : 's'}</>}</Notice> })}
            {down.map((t) => <Notice key={t.name} tone="error" action={<Link className="btn btn-sm" to="/admin/targets">Targets</Link>}><strong>{t.name}</strong> is unreachable {'—'} last seen {hhmm(t.last_seen_at)}</Notice>)}
            {pending.length > 0 && <Notice tone="info" action={<Link className="btn btn-sm" to="/access/approvals">Open queue</Link>}><strong>{pending.length}</strong> access request{pending.length === 1 ? '' : 's'} waiting for you</Notice>}
            {!issues.length && !down.length && !pending.length && <Notice tone="success">All clear. Every stack is healthy and every target is reachable.</Notice>}
          </div>
        </SectionCard>
        <SectionCard icon="bolt" title="My active grants" actions={<Link className="btn btn-ghost btn-xs" to="/access">All grants</Link>}>
          <GrantCards grants={me.active_grants ?? []} empty="No active prod access." />
        </SectionCard>
      </div>
      <div className="grid items-start gap-5 lg:grid-cols-3">
        <SectionCard className="lg:col-span-2" icon="server" title="Targets">
          <Q q={targets} fallback="cards">{(d) => d.items.length ? <TargetCards targets={d.items} stacks={items} /> : <EmptyState icon="server" hint="Targets come from the C8_TARGETS configuration.">No targets configured.</EmptyState>}</Q>
        </SectionCard>
        <SectionCard icon="inbox" title="Pending approvals" actions={<Tip tip="You can approve requests for namespaces you are an approver of, never your own."><span className="badge badge-outline">{pending.length}</span></Tip>}>
          {pending.length ? <ul className="flex flex-col gap-3">{pending.slice(0, 5).map((g) => <li key={g.id} className="flex flex-col gap-2 rounded-box border border-base-300 p-3">
            <div className="flex items-center gap-3"><Avatar name={g.requester} /><div className="min-w-0"><p className="font-medium break-words">{g.requester}</p><p className="text-sm break-words">{g.namespace} on {g.target} {'·'} {g.duration_minutes} min</p></div></div>
            <QuickDecision g={g} canDecide={g.requester !== me.id && me.auth_method !== 'break_glass'} />
          </li>)}</ul>
            : <EmptyState icon="check-circle" hint="Requests for your namespaces show up here, live.">Nothing waiting.</EmptyState>}
          {pending.length > 5 && <Link className="link" to="/access/approvals">See all {pending.length} requests</Link>}
        </SectionCard>
      </div>
      <Q q={inv} fallback="table">{() => <>{[...groups].map(([ns, stacks]) =>
        <SectionCard key={ns} flush icon="folder" title={<Link to={`/n/${ns}`}>{ns}</Link>} subtitle={`${stacks.length} stack${stacks.length === 1 ? '' : 's'}`}
          actions={<Link className="btn btn-ghost btn-xs" to={`/n/${ns}`}>Open namespace<Icon name="arrow-right" className="size-4" /></Link>}>
          <div className="px-5 pb-5">{stacks.length ? <StackTable label={`Stacks in ${ns}`} stacks={stacks} /> : <NoStacks to={`/n/${ns}`} cta="Create a stack" />}</div>
        </SectionCard>)}</>}</Q>
    </Page>
  )
}

const AssetCard = ({ a, ns, stacks }: { a: S['Asset']; ns: string; stacks: S['Stack'][] }) => {
  const mine = stacks.filter((s) => s.asset === a.slug)
  return (
    <li className="card card-border bg-base-100 transition-shadow hover:shadow-md"><div className="card-body p-4 gap-2">
      <div className="flex items-start gap-3">
        <span className="grid place-items-center size-10 rounded-box bg-secondary/15 text-secondary"><Icon name="cube" /></span>
        <div className="min-w-0 flex-1"><Link to={`/n/${ns}/assets/${a.slug}`} className="card-title text-base break-words">{a.name}</Link>
          <p className="text-sm font-mono break-all">{a.image_repo ?? a.slug}</p></div>
        <span className={`badge ${a.managed_by === 'argo' ? 'badge-info' : 'badge-outline'}`}>{a.managed_by}</span>
      </div>
      {a.description && <p className="text-sm line-clamp-2">{a.description}</p>}
      <div className="flex items-center justify-between text-sm border-t border-base-300 pt-2">
        <span className="flex items-center gap-2"><Icon name="layers" className="size-4" />{mine.length} stack{mine.length === 1 ? '' : 's'}</span>
        <span className="flex items-center gap-1" aria-hidden="true">{mine.slice(0, 6).map((s) => <span key={s.id} className={`status ${s.health === 'healthy' ? 'status-success' : s.health === 'degraded' ? 'status-warning' : 'status-neutral'}`} />)}</span>
      </div>
    </div></li>
  )
}

export function Namespace() {
  const { ns = '' } = useParams()
  const me = useMe().data!
  const [tab, setTab] = useState<'overview' | 'audit' | 'settings'>('overview')
  useTitle(ns)
  const n = useGet<S['Namespace']>(['namespace', ns], `/namespaces/${ns}`, { path: { ns } })
  const assets = useGet<{ items: S['Asset'][] }>(['assets', ns], `/namespaces/${ns}/assets`, { path: { ns } })
  const pol = useGet<S['PolicyRules']>(['policy', ns], `/namespaces/${ns}/policy/effective`, { path: { ns } })
  const inv = useInventory()
  const mine = (inv.data?.items ?? []).filter((s) => s.namespace === ns)
  return (
    <Q q={n} fallback="page">{(d) => <Page>
      <PageHeader icon="folder" title={d.display_name || ns} subtitle={d.description ?? `Namespace ${ns}`}
        badges={<>{d.environment_tier && <TierBadge tier={d.environment_tier} large />}<span className="badge badge-lg badge-outline gap-1"><Icon name="user" className="size-4" />Approver group: {d.approver_group ?? 'none'}</span></>}
        actions={<><button type="button" className="btn btn-sm" onClick={() => setTab('settings')}><Icon name="cube" className="size-4" />New asset</button>
          <button type="button" className="btn btn-primary btn-sm" onClick={() => setTab('settings')}><Icon name="plus" className="size-4" />New stack</button></>} />
      <TierBanner tier={d.environment_tier} />
      <StatGrid>
        <StatCard icon="cube" accent="secondary" title="Assets" value={assets.data?.items.length ?? '…'} desc="registered in this namespace" />
        <StatCard icon="layers" accent="primary" title="Stacks" value={mine.length} desc="deployed from manifests" />
        <StatCard accent="success" title="Healthy" value={mine.filter((s) => s.health === 'healthy').length} progress={mine.length ? (mine.filter((s) => s.health === 'healthy').length / mine.length) * 100 : 0} desc="reporting healthy" />
        <StatCard icon="warning" accent={mine.some(bad) ? 'warning' : 'success'} title="Need attention" value={mine.filter(bad).length} desc="degraded or drifted" />
      </StatGrid>
      <Tabs label="Namespace sections" value={tab} onChange={setTab} tabs={[['overview', 'Overview', 'home'], ['audit', 'Audit', 'list'], ['settings', 'Settings', 'sliders']]}>
        {tab === 'overview' && <>
          <SectionCard icon="cube" title="Assets" subtitle="What this namespace runs">
            <Q q={assets} fallback="cards">{(a) => a.items.length ? <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">{a.items.map((x) => <AssetCard key={x.slug} a={x} ns={ns} stacks={mine} />)}</ul>
              : <EmptyState icon="cube" cta="Create an asset" onCta={() => setTab('settings')} hint="An asset is an application or service; stacks deploy it to a target.">No assets.</EmptyState>}</Q>
          </SectionCard>
          <SectionCard flush icon="layers" title="Stacks">
            <div className="px-5 pb-5"><StackTable stacks={mine} loading={!inv.data} empty={<NoStacks />} /></div>
          </SectionCard>
        </>}
        {tab === 'audit' && <SectionCard icon="list" title="Namespace audit"><AuditFeed namespace={ns} /></SectionCard>}
        {tab === 'settings' && <>
          {me.global_admin && <ApproverGroup ns={ns} current={d.approver_group} />}
          <div className="grid gap-5 lg:grid-cols-2"><CreateAsset ns={ns} /><CreateStack ns={ns} /></div>
          <SectionCard collapsible icon="shield" title="Effective policy">
            <Q q={pol}>{(p) => <KeyValueList cols={2} items={[
              { label: 'Allowed registries', value: (p.allowed_registries ?? []).join(', ') || 'any' },
              { label: 'Allowed safety profiles', value: (p.allowed_safety_profiles ?? []).join(', ') || 'any' },
              { label: 'Required labels', value: (p.required_labels ?? []).join(', ') || 'none' },
              { label: 'Privileged containers', value: p.allow_privileged ? 'allowed' : 'denied' }]} />}</Q>
          </SectionCard>
        </>}
      </Tabs>
    </Page>}</Q>
  )
}

export function Asset() {
  const { ns = '', asset = '' } = useParams()
  useTitle(`${asset} — ${ns}`)
  const a = useGet<S['Asset']>(['asset', ns, asset], `/namespaces/${ns}/assets/${asset}`, { path: { ns, asset } })
  const inv = useInventory()
  const mine = (inv.data?.items ?? []).filter((s) => s.namespace === ns && s.asset === asset)
  return (
    <Q q={a} fallback="page">{(d) => <Page>
      <PageHeader icon="cube" title={d.name} subtitle={d.description ?? `Asset ${d.slug} in ${ns}`}
        badges={<><span className="badge badge-outline badge-lg">{d.managed_by}</span>{d.owner && <span className="badge badge-lg badge-ghost gap-1"><Icon name="user" className="size-4" />{d.owner}</span>}</>}
        actions={<Link className="btn btn-sm" to={`/n/${ns}`}><Icon name="arrow-left" className="size-4" />Back to {ns}</Link>} />
      {d.managed_by === 'argo' && <Notice tone="info" title="Register-only">Argo owns this asset. Control 8 shows it but never applies changes to it.</Notice>}
      <div className="grid gap-5 lg:grid-cols-3">
        <SectionCard icon="info" title="Details" className="lg:col-span-1">
          <KeyValueList items={[{ label: 'Managed by', value: d.managed_by, icon: 'shield' }, { label: 'Owner', value: d.owner ?? 'Not set', icon: 'user' },
            { label: 'Image repository', value: <span className="font-mono">{d.image_repo ?? 'Not set'}</span>, icon: 'cube' }, { label: 'Namespace', value: <Link to={`/n/${ns}`}>{ns}</Link>, icon: 'folder' }]} />
        </SectionCard>
        <SectionCard flush icon="layers" title="Stacks" className="lg:col-span-2" subtitle={<>{mine.length} stack{mine.length === 1 ? '' : 's'} deploy this asset <StatusBadgeSummary stacks={mine} /></>}>
          <div className="px-5 pb-5"><StackTable stacks={mine} loading={!inv.data} empty={<NoStacks />} /></div>
        </SectionCard>
      </div>
    </Page>}</Q>
  )
}

const StatusBadgeSummary = ({ stacks }: { stacks: S['Stack'][] }) => stacks.length ? <StatusBadge text={stacks.every((s) => s.health === 'healthy') ? 'healthy' : 'degraded'} /> : null
