import { useQueries } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { AsyncBtn, Avatar, Btn, ConfirmDialog, DataTable, EmptyState, FormError, FormField, Icon, Notice, Page, PageHeader, ProfileBadge, SectionCard, SkCards, TierBadge, Tip, ToggleRow, type Column } from '../components/ui'
import { get, hhmm, Q, useGet, useMe, useTitle, type S } from '../lib'
import { send, useAct } from '../ui'
import { approveErr, denyErr, lines, reqErr } from '../validate'

// GRT-024: requester (own), approver-group member or global_admin. The server decides; the button is shown when plausible.
export function RevokeButton({ g }: { g: S['AccessGrant'] }) {
  const me = useMe().data!
  const act = useAct()
  const [open, setOpen] = useState(false)
  const own = g.requester === me.id
  if (!['requested', 'approved', 'active'].includes(g.status ?? '')) return null
  if (!own && !me.global_admin && !me.approver_for?.includes(g.namespace ?? '')) return null
  const verb = g.status === 'requested' ? 'Cancel' : 'Revoke'
  return <>
    <button className="btn btn-outline btn-error btn-xs" onClick={() => setOpen(true)} aria-label={`${verb} grant ${g.id}`}><Icon name="x" className="size-3.5" />{verb}</button>
    {open && <ConfirmDialog title={`${verb} access for ${g.namespace} on ${g.target}`} confirmLabel={g.status === 'requested' ? 'Cancel request' : 'Revoke'} onClose={() => setOpen(false)}
      description={g.status === 'requested' ? 'The approvers will no longer see this request.' : 'Access ends immediately. A rollout already running finishes or rolls back.'}
      onConfirm={(reason) => act(() => send('POST', '/access-grants/{id}/revoke', { params: { path: { id: g.id } }, body: { reason } }), 'Grant revoked', [['grants']])} />}
  </>
}

// daisyUI "label inside input" control for compact forms.
const Mini = ({ label, children }: { label: string; children: React.ReactNode }) => <label className="input input-sm w-full"><span className="label text-base-content">{label}</span>{children}</label>

// Approve (at the requested length) or deny (reason dialog) in one join: used where there is no room for the full form (overview).
export function QuickDecision({ g, canDecide }: { g: S['AccessGrant']; canDecide: boolean }) {
  const act = useAct()
  const [denying, setDenying] = useState(false)
  const params = { params: { path: { id: g.id } } }
  if (!canDecide) return <Tip tip="You cannot decide your own request."><span className="badge badge-ghost">Your own request</span></Tip>
  return <>
    <div className="join">
      <AsyncBtn className="join-item btn btn-sm btn-success" aria-label={`Approve ${g.id}`} onClick={() => act(() => send('POST', '/access-grants/{id}/approve', { ...params, body: { duration_minutes: g.duration_minutes ?? 60 } }), 'Grant approved', [['grants']])}><Icon name="check" className="size-4" />Approve</AsyncBtn>
      <button className="join-item btn btn-sm btn-error btn-outline" aria-label={`Deny ${g.id}`} onClick={() => setDenying(true)}><Icon name="x" className="size-4" />Deny</button>
    </div>
    {denying && <ConfirmDialog title={`Deny ${g.requester}'s request`} confirmLabel="Deny request" onClose={() => setDenying(false)} description="The requester sees your reason."
      onConfirm={(note) => act(() => send('POST', '/access-grants/{id}/deny', { ...params, body: { note } }), 'Grant denied', [['grants']])} />}
  </>
}

function RequestCard({ g, canDecide }: { g: S['AccessGrant']; canDecide: boolean }) {
  const act = useAct()
  const [mins, setMins] = useState(String(g.duration_minutes ?? 60))
  const [note, setNote] = useState('')
  const [shown, setShown] = useState<string | null>(null)
  const params = { params: { path: { id: g.id } } }
  const approve = () => {
    const e = approveErr(Number(mins), g.duration_minutes ?? 0); setShown(e)
    if (!e) return act(() => send('POST', '/access-grants/{id}/approve', { ...params, body: { duration_minutes: Number(mins), ...(note ? { note } : {}) } }), 'Grant approved', [['grants']])
  }
  const deny = () => {
    const e = denyErr(note); setShown(e)
    if (!e) return act(() => send('POST', '/access-grants/{id}/deny', { ...params, body: { note: note.trim() } }), 'Grant denied', [['grants']])
  }
  return (
    <li className="card card-border bg-base-100 border-l-4 border-l-warning"><div className="card-body p-4 gap-3">
      <div className="flex items-start gap-3">
        <Avatar name={g.requester} />
        <div className="min-w-0 flex-1"><p className="font-semibold break-words">{g.requester}</p><p className="text-sm break-words">{g.namespace} on {g.target}</p></div>
        <div className="flex flex-wrap justify-end gap-1"><TierBadge tier={g.environment_tier ?? 'prod'} /><span className="badge badge-outline gap-1"><Icon name="clock" className="size-3.5" />{g.duration_minutes} min</span></div>
      </div>
      <p className="text-sm bg-base-200 rounded-box p-3 break-words">{g.reason}{g.ticket_ref && <> ({g.ticket_ref})</>}</p>
      {canDecide ? <div className="flex flex-col gap-2">
        <div className="grid gap-2 sm:grid-cols-[8rem_1fr]">
          <Tip tip="Approvers may shorten a grant, never lengthen it." pos="top"><Mini label="Minutes"><input type="number" className="validator w-full" required min={15} max={g.duration_minutes ?? undefined} aria-label={`Minutes for ${g.id}`} value={mins} onChange={(e) => setMins(e.target.value)} /></Mini></Tip>
          <Mini label="Note"><input aria-label={`Note for ${g.id}`} value={note} onChange={(e) => setNote(e.target.value)} placeholder="Required to deny" /></Mini>
        </div>
        <div className="join">
          <AsyncBtn className="join-item btn btn-sm btn-success" onClick={approve} aria-label={`Approve ${g.id}`}><Icon name="check" className="size-4" />Approve</AsyncBtn>
          <AsyncBtn className="join-item btn btn-sm btn-error btn-outline" onClick={deny} aria-label={`Deny ${g.id}`}><Icon name="x" className="size-4" />Deny</AsyncBtn></div>
        <FormError msg={shown} /></div> : <div><Tip tip="You cannot decide your own request."><span className="badge badge-ghost">Your own request</span></Tip></div>}
    </div></li>
  )
}

// 06: requests for namespaces in approver_for, live; a decided card disappears.
export function ApproverQueue() {
  useTitle('Approver queue')
  const me = useMe().data!
  const q = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  return (
    <Page>
      <PageHeader icon="inbox" title="Approver queue" subtitle="Time-boxed prod access requests waiting for you. You can shorten a grant, never lengthen it."
        actions={<Link className="btn btn-sm" to="/access/active"><Icon name="bolt" className="size-4" />Active grants</Link>} />
      {me.auth_method === 'break_glass' && <Notice tone="info">Break-glass sessions cannot approve grants.</Notice>}
      <Q q={q} fallback="cards">{(d) => {
        const rows = d.items.filter((g) => g.status === 'requested' && (me.global_admin || me.approver_for?.includes(g.namespace ?? '')))
        return rows.length ? <ul className="grid gap-4 lg:grid-cols-2">{rows.map((g) => <RequestCard key={g.id} g={g} canDecide={g.requester !== me.id && me.auth_method !== 'break_glass'} />)}</ul>
          : <EmptyState icon="check-circle" hint="New requests for your namespaces appear here without a reload.">No requests waiting.</EmptyState>
      }}</Q>
    </Page>
  )
}

// Pending deployments across visible stacks. There is no list-all endpoint, so this reads each stack's own deployment list (same cache keys as the stack page, live via deployment.updated).
export function Approvals() {
  useTitle('Approvals')
  const act = useAct()
  const [rejecting, setRejecting] = useState<S['Deployment'] | null>(null)
  const inv = useGet<{ items: S['Stack'][] }>(['inventory'], '/inventory')
  const stacks = inv.data?.items ?? []
  const lists = useQueries({ queries: stacks.map((s) => ({ queryKey: ['deployments', s.namespace, s.slug],
    queryFn: () => get<{ items: S['Deployment'][] }>(`/namespaces/${s.namespace}/stacks/${s.slug}/deployments`, { path: { ns: s.namespace, stack: s.slug } }) })) })
  const pending = lists.flatMap((l) => l.data?.items ?? []).filter((d) => d.status === 'pending_approval')
  return (
    <Page>
      <PageHeader icon="check-circle" title="Approvals" subtitle="Deployments waiting for a second person to approve. You can never approve your own." />
      <Q q={inv} fallback="cards">{() => pending.length ? <ul className="grid gap-4 lg:grid-cols-2">{pending.map((d) =>
        <li key={d.id} className="card card-border bg-base-100 border-l-4 border-l-warning"><div className="card-body p-4 gap-3">
          <div className="flex items-start gap-3"><Avatar name={d.requested_by} />
            <div className="min-w-0 flex-1"><p className="font-semibold break-words"><Link to={`/deployments/${d.id}`}>{d.id}</Link></p><p className="text-sm break-words">{d.namespace}/{d.stack} {'·'} requested by {d.requested_by}</p></div>
            <TierBadge tier={d.environment_tier} /></div>
          <div className="flex flex-wrap items-center justify-between gap-2"><ProfileBadge profile={d.safety_profile} />
            <div className="join"><AsyncBtn className="join-item btn btn-sm btn-success" aria-label={`Approve ${d.id}`} onClick={() => act(() => send('POST', '/deployments/{id}/approve', { params: { path: { id: d.id } } }), 'Deployment approved')}><Icon name="check" className="size-4" />Approve</AsyncBtn>
              <button className="join-item btn btn-sm btn-error btn-outline" aria-label={`Reject ${d.id}`} onClick={() => setRejecting(d)}><Icon name="x" className="size-4" />Reject</button></div></div>
        </div></li>)}</ul> : <EmptyState icon="check-circle" hint="Deployments that need approval appear here, live.">No deployments waiting for approval.</EmptyState>}</Q>
      {rejecting && <ConfirmDialog title={`Reject ${rejecting.id}`} confirmLabel="Reject" onClose={() => setRejecting(null)}
        onConfirm={(reason) => act(() => send('POST', '/deployments/{id}/reject', { params: { path: { id: rejecting.id } }, body: { reason } }), 'Deployment rejected')} />}
    </Page>
  )
}

// Approve/reject/cancel on the deployment page.
export function DeploymentActions({ d }: { d: S['Deployment'] }) {
  const act = useAct()
  const [rejecting, setRejecting] = useState(false)
  const p = { params: { path: { id: d.id } } }
  if (d.status !== 'pending_approval' && d.status !== 'queued') return null
  return <div className="flex flex-wrap gap-2">
    {d.status === 'pending_approval' && <><AsyncBtn className="btn btn-sm btn-success" onClick={() => act(() => send('POST', '/deployments/{id}/approve', p), 'Deployment approved')}><Icon name="check" className="size-4" />Approve</AsyncBtn>
      <button className="btn btn-sm btn-error btn-outline" onClick={() => setRejecting(true)}><Icon name="x" className="size-4" />Reject</button></>}
    <AsyncBtn className="btn btn-sm btn-outline" onClick={() => act(() => send('POST', '/deployments/{id}/cancel', p), 'Deployment cancelled')}>Cancel deployment</AsyncBtn>
    {rejecting && <ConfirmDialog title={`Reject ${d.id}`} confirmLabel="Reject" onClose={() => setRejecting(false)}
      onConfirm={(reason) => act(() => send('POST', '/deployments/{id}/reject', { ...p, body: { reason } }), 'Deployment rejected')} />}
  </div>
}

const ROLES = ['viewer', 'operator', 'admin', 'global_admin'] as const
const ROLE_HELP: Record<string, string> = { viewer: 'Read only.', operator: 'Deploy and request access.', admin: 'Manage the namespace.', global_admin: 'Everything, in every namespace.' }
const AdminOnly = ({ title }: { title: string }) => <Page><PageHeader icon="lock" title={title} /><Notice tone="warning">Only administrators can view this page.</Notice></Page>

export function AdminAccess() {
  useTitle('Access administration')
  const me = useMe().data!
  const act = useAct()
  const bindings = useGet<{ items: S['RoleBinding'][] }>(['role-bindings'], '/role-bindings')
  const nss = useGet<{ items: S['Namespace'][] }>(['namespaces'], '/namespaces')
  const tokens = useGet<{ items: S['Token'][] }>(['tokens'], '/tokens')
  const [f, setF] = useState({ idp_group: '', namespace: '', role: 'viewer' })
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [del, setDel] = useState<S['RoleBinding'] | null>(null)
  if (!me.global_admin) return <AdminOnly title="Access administration" />
  const add = async (e: React.FormEvent) => {
    e.preventDefault()
    const m = reqErr(f.idp_group, 'Group') ?? (f.role === 'global_admin' && f.namespace ? 'global_admin applies to all namespaces' : null); setErr(m)
    if (m) return
    setBusy(true)
    try { if (await act(() => send('POST', '/role-bindings', { body: { idp_group: f.idp_group.trim(), role: f.role, namespace: f.namespace || null } }), 'Role binding added', [['role-bindings']])) setF({ ...f, idp_group: '' }) } finally { setBusy(false) }
  }
  const cols: Column<S['RoleBinding']>[] = [
    { key: 'g', header: 'Group', rowHeader: true, cell: (b) => <span className="inline-flex items-center gap-2"><Avatar name={b.idp_group} size="sm" />{b.idp_group}</span> },
    { key: 'ns', header: 'Namespace', cell: (b) => b.namespace ?? 'all' },
    { key: 'role', header: 'Role', cell: (b) => <Tip tip={ROLE_HELP[b.role ?? ""] ?? ''}><span className="badge badge-outline">{b.role}</span></Tip> },
    { key: 'x', header: 'Actions', cell: (b) => <button className="btn btn-outline btn-error btn-xs" aria-label={`Delete binding ${b.idp_group} ${b.role}`} onClick={() => setDel(b)}><Icon name="trash" className="size-3.5" />Delete</button> },
  ]
  const tcols: Column<S['Token']>[] = [
    { key: 'n', header: 'Name', rowHeader: true, cell: (t) => t.name }, { key: 'o', header: 'Owner', cell: (t) => t.owner_sub }, { key: 'ns', header: 'Namespace', cell: (t) => t.namespace ?? 'all' },
    { key: 'r', header: 'Role', cell: (t) => <span className="badge badge-outline">{t.role}</span> }, { key: 'e', header: 'Expires', cell: (t) => hhmm(t.expires_at) },
  ]
  return (
    <Page>
      <PageHeader icon="user" title="Access administration" subtitle="Who can do what. Roles come from identity-provider groups; tokens are configured in the cluster and shown read-only." />
      <div className="grid gap-5 lg:grid-cols-3">
        <SectionCard flush className="lg:col-span-2" icon="user" title="Role bindings">
          <div className="px-5 pb-5"><DataTable label="Role bindings" columns={cols} rows={bindings.data?.items ?? []} rowKey={(b) => b.id ?? ""} loading={!bindings.data}
            empty={<EmptyState icon="user" hint="Bind an identity-provider group to a role on the right.">No role bindings.</EmptyState>} /></div>
        </SectionCard>
        <form onSubmit={add} noValidate aria-label="Add role binding" className="card card-border bg-base-100 shadow-sm self-start"><div className="card-body gap-2 p-5">
          <h2 className="card-title text-base"><Icon name="plus" className="size-5 text-primary" />Add role binding</h2>
          <FormField label="IdP group" vhint="A group is required"><input className="input validator w-full" required value={f.idp_group} onChange={(e) => setF({ ...f, idp_group: e.target.value })} /></FormField>
          <FormField label="Namespace"><select className="select w-full" value={f.namespace} onChange={(e) => setF({ ...f, namespace: e.target.value })}><option value="">All namespaces</option>
            {(nss.data?.items ?? []).map((n) => <option key={n.slug} value={n.slug}>{n.slug}</option>)}</select></FormField>
          <FormField label="Role" hint={ROLE_HELP[f.role]}><select className="select w-full" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>{ROLES.map((r) => <option key={r}>{r}</option>)}</select></FormField>
          <FormError msg={err} />
          <div className="card-actions"><Btn type="submit" busy={busy} className="btn btn-primary btn-sm">Add binding</Btn></div>
        </div></form>
      </div>
      <SectionCard icon="inbox" title="Approver groups" subtitle="Who approves prod access for each namespace.">
        <Q q={nss} fallback="text">{(d) => <ul className="list">{d.items.map((n) => <li key={n.slug} className="list-row items-center px-0"><ApproverRow n={n} /></li>)}</ul>}</Q>
      </SectionCard>
      <SectionCard flush icon="key" title="Configured tokens" subtitle="Read only. Tokens are configured through the environment, never in the UI.">
        <div className="px-5 pb-5"><DataTable label="Tokens" columns={tcols} rows={tokens.data?.items ?? []} rowKey={(t) => t.id ?? ""} loading={!tokens.data} empty={<EmptyState icon="key">No tokens configured.</EmptyState>} /></div>
      </SectionCard>
      {del && <ConfirmDialog title={`Delete binding for ${del.idp_group}`} reason={false} confirmLabel="Delete binding" onClose={() => setDel(null)}
        description={`Members of ${del.idp_group} lose the ${del.role} role${del.namespace ? ` in ${del.namespace}` : ''}.`}
        onConfirm={() => act(() => send('DELETE', '/role-bindings/{id}', { params: { path: { id: del.id } } }), 'Role binding deleted', [['role-bindings']])} />}
    </Page>
  )
}

function ApproverRow({ n }: { n: S['Namespace'] }) {
  const act = useAct()
  const [v, setV] = useState(n.approver_group ?? '')
  const [busy, setBusy] = useState(false)
  useEffect(() => setV(n.approver_group ?? ''), [n.approver_group])
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!v.trim()) return
    setBusy(true)
    try { await act(() => send('PATCH', '/namespaces/{ns}', { params: { path: { ns: n.slug } }, body: { approver_group: v.trim() } }), 'Approver group saved', [['namespaces'], ['namespace', n.slug]]) } finally { setBusy(false) }
  }
  return <form aria-label={`Approver group of ${n.slug}`} onSubmit={save} className="join w-full max-w-xl">
    <label className="input input-sm join-item flex-1"><span className="label text-base-content">{n.slug} <TierBadge tier={n.environment_tier!} /></span><input value={v} onChange={(e) => setV(e.target.value)} /></label>
    <Btn type="submit" busy={busy} className="btn btn-sm btn-primary join-item">Save</Btn></form>
}

const LISTS = ['allowed_registries', 'allowed_k8s_namespaces', 'required_labels', 'allowed_bind_prefixes'] as const
const LIST_HELP: Record<string, string> = { allowed_registries: 'Images must come from one of these registry prefixes.', allowed_k8s_namespaces: 'Stacks may only target these Kubernetes namespaces.',
  required_labels: 'Every workload must carry these labels.', allowed_bind_prefixes: 'Host paths a workload may mount.' }
export function AdminPolicy() {
  useTitle('Global policy')
  const me = useMe().data!
  const act = useAct()
  const q = useGet<S['PolicyRules']>(['policy', 'global'], '/policies/global')
  const [f, setF] = useState<Record<string, any> | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (q.data && !f) setF({ ...Object.fromEntries(LISTS.map((k) => [k, (q.data![k] ?? []).join('\n')])), profiles: q.data.allowed_safety_profiles ?? [],
      protected_db_approval: !!q.data.protected_db_approval, allow_privileged: !!q.data.allow_privileged })
  }, [q.data, f])
  if (!me.global_admin) return <AdminOnly title="Global policy" />
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    const body = { ...Object.fromEntries(LISTS.map((k) => [k, lines(f![k])])), allowed_safety_profiles: f!.profiles,
      protected_db_approval: f!.protected_db_approval, allow_privileged: f!.allow_privileged }
    setBusy(true)
    try { await act(() => send('PUT', '/policies/global', { body }), 'Policy saved', [['policy']]) } finally { setBusy(false) }
  }
  const toggle = (p: string) => setF({ ...f!, profiles: f!.profiles.includes(p) ? f!.profiles.filter((x: string) => x !== p) : [...f!.profiles, p] })
  const list = (k: (typeof LISTS)[number]) => <FormField key={k} label={`${k.replace(/_/g, ' ')} (one per line)`} hint={LIST_HELP[k]}><textarea className="textarea w-full font-mono" rows={3} value={f![k]} onChange={(e) => setF({ ...f!, [k]: e.target.value })} /></FormField>
  return (
    <Page>
      <PageHeader icon="sliders" title="Global policy" subtitle="Rules every revision is checked against before it can be deployed. Namespace policy can only be stricter." />
      <Q q={q} fallback="text">{() => f ? <form onSubmit={save} aria-label="Global policy" className="flex flex-col gap-5 max-w-3xl">
        <SectionCard icon="shield" title="Images and labels">
          {list('allowed_registries')}{list('required_labels')}
        </SectionCard>
        <SectionCard icon="refresh" title="Safety profiles" subtitle="Which ways of deploying are allowed. None selected means any.">
          <fieldset className="fieldset"><legend className="fieldset-legend">Allowed safety profiles</legend>
            <div className="join">{['managed_rollout', 'as_is'].map((p) => <input key={p} type="checkbox" className="join-item btn btn-sm" aria-label={p} checked={f.profiles.includes(p)} onChange={() => toggle(p)} />)}</div></fieldset>
          <fieldset className="fieldset"><legend className="fieldset-legend">Switches</legend>
            <ToggleRow label="Protected database changes need approval" hint="Revisions that touch a database workload wait for an approver." checked={f.protected_db_approval} onChange={(v) => setF({ ...f, protected_db_approval: v })} />
            <ToggleRow label="Allow privileged containers" hint="Off is safer; privileged containers can escape their sandbox." checked={f.allow_privileged} onChange={(v) => setF({ ...f, allow_privileged: v })} /></fieldset>
        </SectionCard>
        <SectionCard collapsible defaultOpen={false} icon="sliders" title="Advanced">{list('allowed_k8s_namespaces')}{list('allowed_bind_prefixes')}</SectionCard>
        <div><Btn type="submit" busy={busy} className="btn btn-primary btn-sm">Save policy</Btn></div>
      </form> : <SkCards n={2} />}</Q>
    </Page>
  )
}
