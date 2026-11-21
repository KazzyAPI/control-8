import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { ProfileBadge, TierBadge } from '../components/Badges'
import { PageHeader, ProdRibbon } from '../components/Parts'
import { useGet, useProdAccess, useTitle, type S } from '../lib'
import { ApiError, Btn, Dialog, Field, FormError, ReasonDialog, send, useAct } from '../ui'
import { deployErr, grantErr, lines, reqErr, revisionErr, slugErr } from '../validate'

const SLUG_PATTERN = '[a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?' // same rule as validate.ts SLUG, for the validator state
const PROFILE_NAME: Record<string, string> = { managed_rollout: 'Managed rollout', as_is: 'As-is', parachute: 'With Parachute' }
const profileName = (p?: string | null) => PROFILE_NAME[p ?? ''] ?? 'none'

// Shared submit wrapper: shows the validation error only after the first submit attempt.
function useForm(validate: () => string | null, run: () => Promise<unknown>) {
  const [shown, setShown] = useState(false)
  const [busy, setBusy] = useState(false)
  const err = validate()
  return {
    error: shown ? err : null,
    busy,
    onSubmit: async (e: FormEvent) => {
      e.preventDefault(); setShown(true)
      if (err) return
      setBusy(true); try { await run() } finally { setBusy(false) }
    },
  }
}

export function CreateAsset({ ns }: { ns: string }) {
  const act = useAct()
  const [f, setF] = useState({ slug: '', name: '', managed_by: 'control8', image_repo: '' })
  const form = useForm(() => slugErr(f.slug) ?? reqErr(f.name, 'Name'), async () => {
    const body: Record<string, string> = { slug: f.slug, name: f.name, managed_by: f.managed_by }
    if (f.image_repo) body.image_repo = f.image_repo
    if (await act(() => send('POST', '/namespaces/{ns}/assets', { params: { path: { ns } }, body }), `Asset ${f.slug} created`, [['assets', ns]])) setF({ ...f, slug: '', name: '', image_repo: '' })
  })
  return (
    <form onSubmit={form.onSubmit} noValidate aria-label="Create asset" className="card card-border bg-base-100"><div className="card-body">
      <h3 className="card-title mt-0">Create asset</h3>
      <Field label="Slug" vhint="Lowercase letters, digits and dashes"><input className="input validator w-full" required pattern={SLUG_PATTERN} value={f.slug} onChange={(e) => setF({ ...f, slug: e.target.value })} /></Field>
      <Field label="Name" vhint="A name is required"><input className="input validator w-full" required value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></Field>
      <Field label="Managed by"><select className="select w-full" value={f.managed_by} onChange={(e) => setF({ ...f, managed_by: e.target.value })}><option value="control8">Control 8</option><option value="argo">Argo</option></select></Field>
      <Field label="Image repository (optional)"><input className="input w-full" value={f.image_repo} onChange={(e) => setF({ ...f, image_repo: e.target.value })} /></Field>
      <FormError msg={form.error} />
      <div className="card-actions"><Btn type="submit" busy={form.busy} className="btn btn-primary btn-sm">Create asset</Btn></div>
    </div></form>
  )
}

export function CreateStack({ ns }: { ns: string }) {
  const act = useAct()
  const nav = useNavigate()
  const assets = useGet<{ items: S['Asset'][] }>(['assets', ns], `/namespaces/${ns}/assets`, { path: { ns } })
  const targets = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  const [f, setF] = useState({ slug: '', asset: '', target: '' })
  const form = useForm(() => slugErr(f.slug) ?? (f.asset && f.target ? null : 'Choose an asset and a target'), async () => {
    const s = await act(() => send<S['Stack']>('POST', '/namespaces/{ns}/stacks', { params: { path: { ns } }, body: f }), `Stack ${f.slug} created`, [['inventory']])
    if (s) nav(`/n/${ns}/s/${f.slug}`)
  })
  return (
    <form onSubmit={form.onSubmit} noValidate aria-label="Create stack" className="card card-border bg-base-100"><div className="card-body">
      <h3 className="card-title mt-0">Create stack</h3>
      <Field label="Slug" vhint="Lowercase letters, digits and dashes"><input className="input validator w-full" required pattern={SLUG_PATTERN} value={f.slug} onChange={(e) => setF({ ...f, slug: e.target.value })} /></Field>
      <Field label="Asset" vhint="Choose an asset"><select className="select validator w-full" required value={f.asset} onChange={(e) => setF({ ...f, asset: e.target.value })}><option value="">Choose…</option>
        {(assets.data?.items ?? []).map((a) => <option key={a.slug} value={a.slug}>{a.name}</option>)}</select></Field>
      <Field label="Target" vhint="Choose a target"><select className="select validator w-full" required value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })}><option value="">Choose…</option>
        {(targets.data?.items ?? []).map((t) => <option key={t.name} value={t.name}>{t.name} ({t.environment_tier})</option>)}</select></Field>
      <FormError msg={form.error} />
      <div className="card-actions"><Btn type="submit" busy={form.busy} className="btn btn-primary btn-sm">Create stack</Btn></div>
    </div></form>
  )
}

// D-50: only global_admin sets the approver group.
export function ApproverGroup({ ns, current }: { ns: string; current?: string | null }) {
  const act = useAct()
  const [v, setV] = useState(current ?? '')
  useEffect(() => setV(current ?? ''), [current])
  const form = useForm(() => reqErr(v, 'Approver group'), async () => {
    await act(() => send('PATCH', '/namespaces/{ns}', { params: { path: { ns } }, body: { approver_group: v.trim() } }), 'Approver group saved', [['namespace', ns]])
  })
  return (
    <form onSubmit={form.onSubmit} noValidate aria-label="Approver group" className="card card-border bg-base-100 max-w-xl mb-4"><div className="card-body">
      <Field label="Approver group" vhint="An approver group is required"><input className="input validator w-full" required value={v} onChange={(e) => setV(e.target.value)} /></Field>
      <FormError msg={form.error} />
      <div className="card-actions"><Btn type="submit" busy={form.busy} className="btn btn-primary btn-sm">Save approver group</Btn></div>
    </div></form>
  )
}

const draftKey = (ns: string, stack: string) => `c8:draft:${ns}/${stack}`
const readDraft = (k: string) => { try { return JSON.parse(sessionStorage.getItem(k) ?? 'null') } catch { return null } }

// 06: new revision form; the draft survives reloads in sessionStorage and is cleared on success. Violations show inline.
export function NewRevision() {
  const { ns = '', stack = '' } = useParams()
  useTitle(`New revision — ${stack}`)
  const nav = useNavigate()
  const act = useAct()
  const key = draftKey(ns, stack)
  const [f, setF] = useState<{ profile: string; manifests: string; timeout: string; digest_poll: boolean; dbs: string }>(
    () => readDraft(key) ?? { profile: '', manifests: '', timeout: '', digest_poll: false, dbs: '' })
  const [violations, setViolations] = useState<S['Violation'][]>([])
  useEffect(() => { try { sessionStorage.setItem(key, JSON.stringify(f)) } catch { /* private mode: no draft */ } }, [key, f])
  const form = useForm(() => revisionErr(f), async () => {
    setViolations([])
    const body: Record<string, unknown> = { safety_profile: f.profile, manifests: f.manifests, digest_poll: f.digest_poll }
    if (f.timeout) body.rollout_timeout_seconds = Number(f.timeout)
    if (lines(f.dbs.replace(/,/g, '\n')).length) body.database_services = lines(f.dbs.replace(/,/g, '\n'))
    try {
      const r = await send<S['Revision']>('POST', '/namespaces/{ns}/stacks/{stack}/revisions', { params: { path: { ns, stack } }, body })
      try { sessionStorage.removeItem(key) } catch { /* ignore */ }
      nav(`/n/${ns}/s/${stack}/revisions/${r.number}`)
    } catch (e) {
      if (e instanceof ApiError && e.problem.violations) setViolations(e.problem.violations)
      await act(() => Promise.reject(e))
    }
  })
  return (
    <>
      <PageHeader title="New revision" actions={<Link className="btn btn-sm" to={`/n/${ns}/s/${stack}`}>Back to {stack}</Link>} />
      <form onSubmit={form.onSubmit} noValidate aria-label="New revision" className="max-w-3xl">
        <Field label="Safety profile" vhint="Choose a safety profile"><select className="select validator w-full" required value={f.profile} onChange={(e) => setF({ ...f, profile: e.target.value })}>
          <option value="">Choose…</option><option value="managed_rollout">Managed rollout</option><option value="as_is">As-is — no safety net</option></select></Field>
        <Field label="Manifests (YAML)" vhint="Manifests are required"><textarea className="textarea validator w-full font-mono" required rows={14} value={f.manifests} onChange={(e) => setF({ ...f, manifests: e.target.value })} spellCheck={false} /></Field>
        <Field label="Rollout timeout in seconds (optional)" vhint="Digits only"><input className="input validator w-full" inputMode="numeric" pattern="[0-9]*" value={f.timeout} onChange={(e) => setF({ ...f, timeout: e.target.value })} /></Field>
        <fieldset className="fieldset"><label className="label text-base-content gap-3"><input type="checkbox" className="toggle toggle-sm" checked={f.digest_poll} onChange={(e) => setF({ ...f, digest_poll: e.target.checked })} /> Follow image digests (managed rollout only)</label></fieldset>
        <Field label="Database workloads (comma separated, optional)"><input className="input w-full" value={f.dbs} onChange={(e) => setF({ ...f, dbs: e.target.value })} /></Field>
        <FormError msg={form.error} />
        {violations.length > 0 && <div role="alert" className="alert alert-error alert-vertical items-start my-2"><p className="my-0 font-semibold">Policy violations:</p><ul className="list">{violations.map((v, i) => <li key={i} className="list-row">{v.rule}{v.service && ` (${v.service})`}: {v.message}</li>)}</ul></div>}
        <Btn type="submit" busy={form.busy} className="btn btn-primary mt-2">Create revision</Btn>
      </form>
    </>
  )
}

// UI-013/014: deploy dialog.
export function DeployDialog({ s, r, onClose }: { s: S['Stack']; r: S['Revision']; onClose: () => void }) {
  const act = useAct()
  const nav = useNavigate()
  const prod = s.environment_tier === 'prod'
  const [f, setF] = useState({ picked: '', typed: '', ack: false, reason: '' })
  const form = useForm(() => deployErr({ profile: r.safety_profile ?? null, picked: f.picked, tier: s.environment_tier, typed: f.typed, slug: s.namespace!, ack: f.ack }), async () => {
    const d = await act(() => send<S['Deployment']>('POST', '/namespaces/{ns}/stacks/{stack}/deployments', {
      params: { path: { ns: s.namespace, stack: s.slug } },
      body: { revision: r.number, confirm_safety_profile: f.picked, confirm_environment_tier: s.environment_tier, ...(f.reason ? { reason: f.reason } : {}) } }),
      'Deployment requested', [['deployments', s.namespace, s.slug]])
    if (d) nav(`/deployments/${d.id}`)
  })
  const changed = s.state === 'active' && s.safety_profile && r.safety_profile && s.safety_profile !== r.safety_profile
  return (
    <Dialog title={`Deploy revision ${r.number}`} onClose={onClose}>
      <form onSubmit={form.onSubmit} noValidate>
        <p className="flex flex-wrap gap-2"><TierBadge tier={s.environment_tier} large /> <ProfileBadge profile={r.safety_profile} large /></p>
        {prod && <ProdRibbon tier="prod" />}
        {changed && <div className="alert alert-warning my-2"><span className="font-semibold">Safety profile changes: {profileName(s.safety_profile)} {'→'} {profileName(r.safety_profile)}</span></div>}
        <fieldset className="fieldset"><legend className="fieldset-legend">Safety profile of this revision</legend>
          {(['managed_rollout', 'as_is'] as const).map((p) => <label key={p} className="label text-base-content gap-2"><input type="radio" name="profile" className="radio radio-sm" checked={f.picked === p} onChange={() => setF({ ...f, picked: p })} /> {profileName(p)}</label>)}
        </fieldset>
        {r.safety_profile === 'as_is' && <fieldset className="fieldset"><label className="label text-base-content gap-2"><input type="checkbox" className="checkbox checkbox-sm" checked={f.ack} onChange={(e) => setF({ ...f, ack: e.target.checked })} /> I understand there is no automatic rollback</label></fieldset>}
        {prod && <Field label={<>Type <code>{s.namespace}</code> to confirm this production deploy</>} vhint={`Type ${s.namespace} exactly`}>
          <input className="input validator w-full" required pattern={s.namespace} value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></Field>}
        <Field label="Reason (optional)"><input className="input w-full" value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        <FormError msg={form.error} />
        <div className="modal-action"><Btn type="submit" busy={form.busy} className={`btn btn-sm ${prod ? 'btn-error' : 'btn-primary'}`}>Deploy rev {r.number} to {prod ? 'PROD' : 'NON-PROD'} {'—'} {profileName(r.safety_profile)}</Btn> <button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button></div>
      </form>
    </Dialog>
  )
}

export function ImageUpdateDialog({ s, workload, container, current, onClose }: { s: S['Stack']; workload: string; container: string; current: string; onClose: () => void }) {
  const act = useAct()
  const nav = useNavigate()
  const prod = s.environment_tier === 'prod'
  const [f, setF] = useState({ image: current, reason: '', typed: '' })
  const form = useForm(() => reqErr(f.image, 'Image') ?? reqErr(f.reason, 'Reason') ?? (prod && f.typed !== s.namespace ? `Type "${s.namespace}" to confirm the production change` : null), async () => {
    const d = await act(() => send<S['Deployment']>('POST', '/namespaces/{ns}/stacks/{stack}/image-updates', {
      params: { path: { ns: s.namespace, stack: s.slug } },
      body: { workload, container, image: f.image.trim(), reason: f.reason.trim(), confirm_environment_tier: s.environment_tier } }),
      'Image update requested', [['deployments', s.namespace, s.slug]])
    if (d) nav(`/deployments/${d.id}`)
  })
  return (
    <Dialog title={`Update image of ${workload} / ${container}`} onClose={onClose}>
      <form onSubmit={form.onSubmit} noValidate>
        <p className="flex flex-wrap gap-2"><TierBadge tier={s.environment_tier} large /> <ProfileBadge profile={s.safety_profile} large /></p>
        {prod && <ProdRibbon tier="prod" />}
        <Field label="New image (same repository)" vhint="An image is required"><input className="input validator w-full" required value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} /></Field>
        <Field label="Reason" vhint="A reason is required"><input className="input validator w-full" required value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        {prod && <Field label={<>Type <code>{s.namespace}</code> to confirm</>} vhint={`Type ${s.namespace} exactly`}>
          <input className="input validator w-full" required pattern={s.namespace} value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></Field>}
        <FormError msg={form.error} />
        <div className="modal-action"><Btn type="submit" busy={form.busy} className={`btn btn-sm ${prod ? 'btn-error' : 'btn-primary'}`}>Update image{prod ? ' on PROD' : ''}</Btn> <button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button></div>
      </form>
    </Dialog>
  )
}

// New revision, image updates per container and teardown. Prod buttons follow the live grant state (UI-016).
export function StackActions({ s }: { s: S['Stack'] }) {
  const act = useAct()
  const nav = useNavigate()
  const a = useProdAccess(s.namespace!, s.target!, s.environment_tier)
  const [dlg, setDlg] = useState<{ workload: string; container: string; image: string } | 'teardown' | null>(null)
  const draft = s.state === 'draft'
  const canImage = s.runtime === 'kubernetes' && s.safety_profile === 'managed_rollout' && s.state === 'active'
  const rows = canImage ? (s.services ?? []).flatMap((v) => (v.containers ?? []).map((c) => ({ workload: v.name!, container: c.name!, image: c.image ?? '' }))) : []
  return (
    <section aria-labelledby="h-act">
      <h2 id="h-act" className="mt-0">Actions</h2>
      <div className="flex flex-wrap gap-2 my-2"><Link className="btn btn-sm btn-primary" to={`/n/${s.namespace}/s/${s.slug}/revisions/new`}>New revision</Link>
        <button className="btn btn-sm btn-outline btn-error" disabled={!draft && !a.allowed} onClick={() => setDlg('teardown')}>{draft ? 'Delete draft' : 'Teardown'}</button>
      </div>
      {rows.length > 0 && <ul className="list bg-base-100 rounded-box border border-base-300">{rows.map((r) => <li key={`${r.workload}/${r.container}`} className="list-row items-center"><div>{r.workload} / {r.container}: {r.image}</div>
        <button className="btn btn-sm" disabled={!a.allowed} aria-label={`Update image of ${r.workload} ${r.container}`} onClick={() => setDlg(r)}>Update image</button></li>)}</ul>}
      {dlg === 'teardown' && <ReasonDialog title={draft ? 'Delete draft stack' : `Tear down ${s.slug}`} submit={draft ? 'Delete draft' : 'Tear down'}
        typed={s.environment_tier === 'prod' && !draft ? s.namespace : undefined} onClose={() => setDlg(null)}
        onSubmit={async (reason) => {
          const d = await act(() => send<S['Deployment']>('DELETE', '/namespaces/{ns}/stacks/{stack}', { params: { path: { ns: s.namespace, stack: s.slug } },
            body: { reason, confirm_environment_tier: s.environment_tier } }), draft ? 'Draft deleted' : 'Teardown requested', [['inventory'], ['deployments', s.namespace, s.slug]])
          if (d && (d as any).id) nav(`/deployments/${(d as any).id}`); else if (d && draft) nav(`/n/${s.namespace}`)
          return d
        }} />}
      {dlg && dlg !== 'teardown' && <ImageUpdateDialog s={s} {...dlg} current={dlg.image} onClose={() => setDlg(null)} />}
    </section>
  )
}

// `/access/request?namespace=&target=` (also opened by a grant_required response).
export function RequestAccess() {
  useTitle('Request access')
  const [q] = useSearchParams()
  const act = useAct()
  const nav = useNavigate()
  const nss = useGet<{ items: S['Namespace'][] }>(['namespaces'], '/namespaces')
  const targets = useGet<{ items: S['Target'][] }>(['targets'], '/targets')
  const [f, setF] = useState({ namespace: q.get('namespace') ?? '', target: q.get('target') ?? '', reason: '', ticket_ref: '', duration: '60', starts_at: '' })
  const form = useForm(() => grantErr({ ...f, duration: Number(f.duration) }), async () => {
    const body: Record<string, unknown> = { namespace: f.namespace, target: f.target, reason: f.reason.trim(), duration_minutes: Number(f.duration) }
    if (f.ticket_ref) body.ticket_ref = f.ticket_ref
    if (f.starts_at) body.starts_at = new Date(f.starts_at).toISOString()
    if (await act(() => send('POST', '/access-grants', { body }), 'Access requested', [['grants']])) nav('/access')
  })
  return (
    <>
      <PageHeader title="Request access" />
      <form onSubmit={form.onSubmit} noValidate aria-label="Request prod access" className="card card-border bg-base-100 max-w-2xl"><div className="card-body">
        <Field label="Namespace" vhint="Choose a namespace"><select className="select validator w-full" required value={f.namespace} onChange={(e) => setF({ ...f, namespace: e.target.value })}><option value="">Choose…</option>
          {(nss.data?.items ?? []).map((n) => <option key={n.slug} value={n.slug}>{n.slug}</option>)}</select></Field>
        <Field label="Target" vhint="Choose a target"><select className="select validator w-full" required value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })}><option value="">Choose…</option>
          {(targets.data?.items ?? []).map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}</select></Field>
        <Field label="Reason (10 to 500 characters)" vhint="At least 10 characters"><textarea className="textarea validator w-full" required minLength={10} rows={3} value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        <Field label="Ticket reference (optional)"><input className="input w-full" value={f.ticket_ref} onChange={(e) => setF({ ...f, ticket_ref: e.target.value })} /></Field>
        <Field label="Duration in minutes" vhint="At least 15 minutes" hint="Approvers may shorten a grant, never lengthen it.">
          <input className="input validator w-full" inputMode="numeric" required pattern="[0-9]+" value={f.duration} onChange={(e) => setF({ ...f, duration: e.target.value })} /></Field>
        <div>
          <input type="range" className="range range-primary range-sm w-full" min={15} max={480} step={15} aria-label="Duration slider" aria-valuetext={`${f.duration} minutes`}
            value={Math.min(480, Math.max(15, Number(f.duration) || 15))} onChange={(e) => setF({ ...f, duration: e.target.value })} />
          <div className="relative h-5 text-xs" aria-hidden="true">
            {([['15 min', 'left-0'], ['1 h', 'left-[9.7%] -translate-x-1/2'], ['2 h', 'left-[22.6%] -translate-x-1/2'], ['4 h', 'left-[48.4%] -translate-x-1/2'], ['8 h', 'right-0']] as const).map(([t, pos]) =>
              <span key={t} className={`absolute whitespace-nowrap ${pos}`}>{t}</span>)}
          </div>
        </div>
        <Field label="Start (optional, within 7 days)"><input className="input w-full" type="datetime-local" value={f.starts_at} onChange={(e) => setF({ ...f, starts_at: e.target.value })} /></Field>
        <FormError msg={form.error} />
        <div className="card-actions"><Btn type="submit" busy={form.busy} className="btn btn-primary">Request access</Btn></div>
      </div></form>
    </>
  )
}
