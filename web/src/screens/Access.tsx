import { useQueries } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { ProfileBadge, TierBadge } from '../components/Badges'
import { Empty, PageHeader } from '../components/Parts'
import { get, hhmm, Q, Table, useGet, useMe, useTitle, type S } from '../lib'
import { AsyncBtn, Btn, Field, FormError, ReasonDialog, send, useAct } from '../ui'
import { approveErr, denyErr, lines, reqErr } from '../validate'

// GRT-024: requester (own), approver-group member or global_admin. The server decides; the button is shown when plausible.
export function RevokeButton({ g }: { g: S['AccessGrant'] }) {
  const me = useMe().data!
  const act = useAct()
  const [open, setOpen] = useState(false)
  const own = g.requester === me.id
  if (!['requested', 'approved', 'active'].includes(g.status ?? '')) return null
  if (!own && !me.global_admin && !me.approver_for?.includes(g.namespace ?? '')) return null
  return <>
    <button className="btn btn-outline btn-error btn-xs" onClick={() => setOpen(true)} aria-label={`${g.status === 'requested' ? 'Cancel' : 'Revoke'} grant ${g.id}`}>{g.status === 'requested' ? 'Cancel' : 'Revoke'}</button>
    {open && <ReasonDialog title={`${g.status === 'requested' ? 'Cancel' : 'Revoke'} access for ${g.namespace} on ${g.target}`} submit={g.status === 'requested' ? 'Cancel request' : 'Revoke'} onClose={() => setOpen(false)}
      onSubmit={(reason) => act(() => send('POST', '/access-grants/{id}/revoke', { params: { path: { id: g.id } }, body: { reason } }), 'Grant revoked', [['grants']])} />}
  </>
}

// daisyUI "label inside input" control for compact table forms.
const Mini = ({ label, children }: { label: string; children: React.ReactNode }) => <label className="input input-sm"><span className="label text-base-content">{label}</span>{children}</label>

function QueueRow({ g, canDecide }: { g: S['AccessGrant']; canDecide: boolean }) {
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
    <tr>
      <th scope="row">{g.namespace}</th><td>{g.target}</td><td>{g.requester}</td><td>{g.reason}{g.ticket_ref && <> ({g.ticket_ref})</>}</td><td>{g.duration_minutes} min</td>
      <td>{canDecide ? <div className="flex flex-wrap items-center gap-2">
        <Mini label="Minutes"><input type="number" className="validator w-16" required min={15} max={g.duration_minutes ?? undefined} aria-label={`Minutes for ${g.id}`} value={mins} onChange={(e) => setMins(e.target.value)} /></Mini>
        <Mini label="Note"><input className="w-40" aria-label={`Note for ${g.id}`} value={note} onChange={(e) => setNote(e.target.value)} /></Mini>
        <div className="join">
          <AsyncBtn className="join-item btn btn-sm btn-success" onClick={approve} aria-label={`Approve ${g.id}`}>Approve</AsyncBtn>
          <AsyncBtn className="join-item btn btn-sm btn-error btn-outline" onClick={deny} aria-label={`Deny ${g.id}`}>Deny</AsyncBtn></div>
        <FormError msg={shown} /></div> : <span className="badge badge-ghost">Your own request</span>}</td>
    </tr>
  )
}

// 06: requests for namespaces in approver_for, live; a decided row disappears.
export function ApproverQueue() {
  useTitle('Approver queue')
  const me = useMe().data!
  const q = useGet<{ items: S['AccessGrant'][] }>(['grants', 'all'], '/access-grants')
  return (
    <>
      <PageHeader title="Approver queue" />
      {me.auth_method === 'break_glass' && <div className="alert alert-info my-2"><span>Break-glass sessions cannot approve grants.</span></div>}
      <Q q={q} fallback="table">{(d) => {
        const rows = d.items.filter((g) => g.status === 'requested' && (me.global_admin || me.approver_for?.includes(g.namespace ?? '')))
        return rows.length ? <Table label="Access requests" head={['Namespace', 'Target', 'Requester', 'Reason', 'Requested', 'Decision']}>
          {rows.map((g) => <QueueRow key={g.id} g={g} canDecide={g.requester !== me.id && me.auth_method !== 'break_glass'} />)}</Table> : <Empty>No requests waiting.</Empty>
      }}</Q>
    </>
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
    <>
      <PageHeader title="Approvals" />
      <Q q={inv} fallback="table">{() => pending.length ? <Table label="Pending deployments" head={['Deployment', 'Stack', 'Safety profile', 'Requested by', 'Actions']}>
        {pending.map((d) => <tr key={d.id}>
          <th scope="row"><Link to={`/deployments/${d.id}`}>{d.id}</Link></th><td>{d.namespace}/{d.stack} <TierBadge tier={d.environment_tier} /></td>
          <td><ProfileBadge profile={d.safety_profile} /></td><td>{d.requested_by}</td>
          <td><div className="join"><AsyncBtn className="join-item btn btn-sm btn-success" aria-label={`Approve ${d.id}`} onClick={() => act(() => send('POST', '/deployments/{id}/approve', { params: { path: { id: d.id } } }), 'Deployment approved')}>Approve</AsyncBtn>
            <button className="join-item btn btn-sm btn-error btn-outline" aria-label={`Reject ${d.id}`} onClick={() => setRejecting(d)}>Reject</button></div></td></tr>)}
      </Table> : <Empty>No deployments waiting for approval.</Empty>}</Q>
      {rejecting && <ReasonDialog title={`Reject ${rejecting.id}`} submit="Reject" onClose={() => setRejecting(null)}
        onSubmit={(reason) => act(() => send('POST', '/deployments/{id}/reject', { params: { path: { id: rejecting.id } }, body: { reason } }), 'Deployment rejected')} />}
    </>
  )
}

// Approve/reject/cancel on the deployment page.
export function DeploymentActions({ d }: { d: S['Deployment'] }) {
  const act = useAct()
  const [rejecting, setRejecting] = useState(false)
  const p = { params: { path: { id: d.id } } }
  if (d.status !== 'pending_approval' && d.status !== 'queued') return null
  return <div className="flex flex-wrap gap-2 my-2">
    {d.status === 'pending_approval' && <><AsyncBtn className="btn btn-sm btn-success" onClick={() => act(() => send('POST', '/deployments/{id}/approve', p), 'Deployment approved')}>Approve</AsyncBtn>
      <button className="btn btn-sm btn-error btn-outline" onClick={() => setRejecting(true)}>Reject</button></>}
    <AsyncBtn className="btn btn-sm btn-outline" onClick={() => act(() => send('POST', '/deployments/{id}/cancel', p), 'Deployment cancelled')}>Cancel deployment</AsyncBtn>
    {rejecting && <ReasonDialog title={`Reject ${d.id}`} submit="Reject" onClose={() => setRejecting(false)}
      onSubmit={(reason) => act(() => send('POST', '/deployments/{id}/reject', { ...p, body: { reason } }), 'Deployment rejected')} />}
  </div>
}

const ROLES = ['viewer', 'operator', 'admin', 'global_admin'] as const
const AdminOnly = ({ title }: { title: string }) => <><PageHeader title={title} /><div className="alert alert-warning"><span>Only administrators can view this page.</span></div></>

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
  if (!me.global_admin) return <AdminOnly title="Access administration" />
  const add = async (e: React.FormEvent) => {
    e.preventDefault()
    const m = reqErr(f.idp_group, 'Group') ?? (f.role === 'global_admin' && f.namespace ? 'global_admin applies to all namespaces' : null); setErr(m)
    if (m) return
    setBusy(true)
    try { if (await act(() => send('POST', '/role-bindings', { body: { idp_group: f.idp_group.trim(), role: f.role, namespace: f.namespace || null } }), 'Role binding added', [['role-bindings']])) setF({ ...f, idp_group: '' }) } finally { setBusy(false) }
  }
  return (
    <>
      <PageHeader title="Access administration" />
      <h2>Role bindings</h2>
      <Q q={bindings} fallback="table">{(d) => d.items.length ? <Table label="Role bindings" head={['Group', 'Namespace', 'Role', 'Actions']}>
        {d.items.map((b) => <tr key={b.id}><th scope="row">{b.idp_group}</th><td>{b.namespace ?? 'all'}</td><td><span className="badge badge-outline">{b.role}</span></td>
          <td><AsyncBtn className="btn btn-outline btn-error btn-xs" aria-label={`Delete binding ${b.idp_group} ${b.role}`} onClick={() => act(() => send('DELETE', '/role-bindings/{id}', { params: { path: { id: b.id } } }), 'Role binding deleted', [['role-bindings']])}>Delete</AsyncBtn></td></tr>)}</Table> : <Empty>No role bindings.</Empty>}</Q>
      <form onSubmit={add} noValidate aria-label="Add role binding" className="card card-border bg-base-100 max-w-xl"><div className="card-body">
        <h3 className="card-title mt-0">Add role binding</h3>
        <Field label="IdP group" vhint="A group is required"><input className="input validator w-full" required value={f.idp_group} onChange={(e) => setF({ ...f, idp_group: e.target.value })} /></Field>
        <Field label="Namespace"><select className="select w-full" value={f.namespace} onChange={(e) => setF({ ...f, namespace: e.target.value })}><option value="">All namespaces</option>
          {(nss.data?.items ?? []).map((n) => <option key={n.slug} value={n.slug}>{n.slug}</option>)}</select></Field>
        <Field label="Role"><select className="select w-full" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>{ROLES.map((r) => <option key={r}>{r}</option>)}</select></Field>
        <FormError msg={err} />
        <div className="card-actions"><Btn type="submit" busy={busy} className="btn btn-primary btn-sm">Add binding</Btn></div>
      </div></form>
      <h2>Approver groups</h2>
      <Q q={nss} fallback="text">{(d) => <ul className="list bg-base-100 rounded-box border border-base-300">{d.items.map((n) => <li key={n.slug} className="list-row"><ApproverRow n={n} /></li>)}</ul>}</Q>
      <h2>Configured tokens</h2>
      <Q q={tokens} fallback="table">{(d) => d.items.length ? <Table label="Tokens" head={['Name', 'Owner', 'Namespace', 'Role', 'Expires']}>
        {d.items.map((t) => <tr key={t.id}><th scope="row">{t.name}</th><td>{t.owner_sub}</td><td>{t.namespace ?? 'all'}</td><td>{t.role}</td><td>{hhmm(t.expires_at)}</td></tr>)}</Table> : <Empty>No tokens configured.</Empty>}</Q>
    </>
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
  return <form aria-label={`Approver group of ${n.slug}`} onSubmit={save} className="join w-full max-w-xl my-0">
    <label className="input input-sm join-item flex-1"><span className="label text-base-content">{n.slug} <TierBadge tier={n.environment_tier!} /></span><input value={v} onChange={(e) => setV(e.target.value)} /></label>
    <Btn type="submit" busy={busy} className="btn btn-sm btn-primary join-item">Save</Btn></form>
}

const LISTS = ['allowed_registries', 'allowed_k8s_namespaces', 'required_labels', 'allowed_bind_prefixes'] as const
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
  return (
    <>
      <PageHeader title="Global policy" />
      <Q q={q} fallback="text">{() => f ? <form onSubmit={save} aria-label="Global policy" className="max-w-xl">
        {LISTS.map((k) => <Field key={k} label={`${k.replace(/_/g, ' ')} (one per line)`}><textarea className="textarea w-full font-mono" rows={3} value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} /></Field>)}
        <fieldset className="fieldset"><legend className="fieldset-legend">Allowed safety profiles (none checked = any)</legend>
          {['managed_rollout', 'as_is'].map((p) => <label key={p} className="label text-base-content gap-2"><input type="checkbox" className="checkbox checkbox-sm" checked={f.profiles.includes(p)} onChange={() => toggle(p)} /> {p}</label>)}</fieldset>
        <fieldset className="fieldset"><legend className="fieldset-legend">Switches</legend>
          <label className="label text-base-content gap-3"><input type="checkbox" className="toggle toggle-sm" checked={f.protected_db_approval} onChange={(e) => setF({ ...f, protected_db_approval: e.target.checked })} /> Protected database changes need approval</label>
          <label className="label text-base-content gap-3"><input type="checkbox" className="toggle toggle-sm" checked={f.allow_privileged} onChange={(e) => setF({ ...f, allow_privileged: e.target.checked })} /> Allow privileged containers</label></fieldset>
        <Btn type="submit" busy={busy} className="btn btn-primary btn-sm mt-2">Save policy</Btn>
      </form> : <></>}</Q>
    </>
  )
}
