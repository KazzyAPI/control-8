import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { Btn, ConfirmDialog, FormError, FormField as Field, Icon, Modal, ModalActions, Notice, Page, PageHeader, ProfileBadge, SectionCard, TierBadge, TierBanner, ToggleRow, Tip } from '../components/ui'
import { useGet, useProdAccess, useTitle, type S } from '../lib'
import { ApiError, send, useAct } from '../ui'
import { deployErr, grantErr, lines, reqErr, revisionErr, slugErr } from '../validate'

const SLUG_PATTERN = '[a-z0-9]([a-z0-9\\-]{0,61}[a-z0-9])?' // same rule as validate.ts SLUG, for the validator state
const PROFILE_NAME: Record<string, string> = { managed_rollout: 'Managed rollout', as_is: 'As-is', parachute: 'With Parachute' }
const profileName = (p?: string | null) => PROFILE_NAME[p ?? ''] ?? 'none'
const PROFILE_HELP: Record<string, string> = { managed_rollout: 'Rolls out gradually, watches health, and rolls back on its own if it fails.', as_is: 'Applied exactly as written. No safety net and no automatic rollback.' }

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
    <form onSubmit={form.onSubmit} noValidate aria-label="Create asset" className="card card-border bg-base-100 shadow-sm"><div className="card-body gap-2 p-5">
      <h2 className="card-title text-base"><Icon name="cube" className="size-5 text-primary" />Create asset</h2>
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
    <form onSubmit={form.onSubmit} noValidate aria-label="Create stack" className="card card-border bg-base-100 shadow-sm"><div className="card-body gap-2 p-5">
      <h2 className="card-title text-base"><Icon name="layers" className="size-5 text-primary" />Create stack</h2>
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
    <form onSubmit={form.onSubmit} noValidate aria-label="Approver group" className="card card-border bg-base-100 shadow-sm"><div className="card-body gap-2 p-5">
      <h2 className="card-title text-base"><Icon name="user" className="size-5 text-primary" />Approver group</h2>
      <Field label="Approver group" hint="Members of this group approve prod access for the namespace." vhint="An approver group is required"><input className="input validator w-full" required value={v} onChange={(e) => setV(e.target.value)} /></Field>
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
    <Page>
      <PageHeader icon="document" title="New revision" subtitle={<>Manifests for <Link to={`/n/${ns}/s/${stack}`}>{stack}</Link> are checked against policy when you create the revision; nothing is deployed yet.</>}
        actions={<Link className="btn btn-sm" to={`/n/${ns}/s/${stack}`}><Icon name="arrow-left" className="size-4" />Back to {stack}</Link>} />
      <form onSubmit={form.onSubmit} noValidate aria-label="New revision" className="flex flex-col gap-5 max-w-3xl">
        <SectionCard icon="shield" title="How to deploy">
          <Field label="Safety profile" vhint="Choose a safety profile" hint="Managed rollout watches health and rolls back on its own. As-is applies exactly what you wrote."><select className="select validator w-full" required value={f.profile} onChange={(e) => setF({ ...f, profile: e.target.value })}>
            <option value="">Choose…</option><option value="managed_rollout">Managed rollout</option><option value="as_is">As-is — no safety net</option></select></Field>
          <Field label="Rollout timeout in seconds (optional)" vhint="Digits only"><input className="input validator w-full" inputMode="numeric" pattern="[0-9]*" value={f.timeout} onChange={(e) => setF({ ...f, timeout: e.target.value })} /></Field>
          <ToggleRow label="Follow image digests" hint="Managed rollout only. Reports newer digests; prod stacks never update on their own." checked={f.digest_poll} onChange={(v) => setF({ ...f, digest_poll: v })} />
        </SectionCard>
        <SectionCard icon="document" title="Manifests">
          <Field label="Manifests (YAML)" vhint="Manifests are required"><textarea className="textarea validator w-full font-mono" required rows={14} value={f.manifests} onChange={(e) => setF({ ...f, manifests: e.target.value })} spellCheck={false} /></Field>
          <Field label="Database workloads (comma separated, optional)" hint="Protected: changes to these can need approval."><input className="input w-full" value={f.dbs} onChange={(e) => setF({ ...f, dbs: e.target.value })} /></Field>
        </SectionCard>
        <FormError msg={form.error} />
        {violations.length > 0 && <Notice tone="error" role="alert" title="Policy violations"><ul className="list">{violations.map((v, i) => <li key={i} className="list-row px-0">{v.rule}{v.service && ` (${v.service})`}: {v.message}</li>)}</ul></Notice>}
        <div><Btn type="submit" busy={form.busy} className="btn btn-primary">Create revision</Btn></div>
      </form>
    </Page>
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
    <Modal title={`Deploy revision ${r.number}`} icon="deploy" onClose={onClose} wide>
      <form onSubmit={form.onSubmit} noValidate className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2"><TierBadge tier={s.environment_tier} large /> <ProfileBadge profile={r.safety_profile} large /> <span className="text-sm">to {s.namespace}/{s.slug} on {s.target}</span></div>
        <TierBanner tier={s.environment_tier} />
        {changed && <Notice tone="warning" title={`Safety profile changes: ${profileName(s.safety_profile)} → ${profileName(r.safety_profile)}`}>The new revision is deployed the new way from now on.</Notice>}
        <fieldset className="fieldset"><legend className="fieldset-legend">Confirm the safety profile of this revision</legend>
          <div className="grid gap-2 sm:grid-cols-2">{(['managed_rollout', 'as_is'] as const).map((p) => <label key={p} className="flex items-start gap-3 rounded-box border border-base-300 p-3 cursor-pointer has-[:checked]:border-primary has-[:checked]:bg-primary/10">
            <input type="radio" name="profile" className="radio radio-primary radio-sm mt-1" aria-label={profileName(p)} aria-describedby={`pf-${p}`} checked={f.picked === p} onChange={() => setF({ ...f, picked: p })} />
            <span><span className="block font-semibold">{profileName(p)}</span><span id={`pf-${p}`} className="block text-sm">{PROFILE_HELP[p]}</span></span></label>)}</div>
        </fieldset>
        {r.safety_profile === 'as_is' && <label className="flex items-start gap-3 rounded-box bg-warning/15 p-3 cursor-pointer"><input type="checkbox" className="checkbox checkbox-warning checkbox-sm mt-0.5" checked={f.ack} onChange={(e) => setF({ ...f, ack: e.target.checked })} /><span className="text-sm">I understand there is no automatic rollback</span></label>}
        {prod && <Field label={<>Type <code>{s.namespace}</code> to confirm this production deploy</>} vhint={`Type ${s.namespace} exactly`}>
          <input className="input validator w-full" required pattern={s.namespace} value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></Field>}
        <Field label="Reason (optional)"><input className="input w-full" value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        <FormError msg={form.error} />
        <ModalActions><button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button><Btn type="submit" busy={form.busy} className={`btn btn-sm ${prod ? 'btn-error' : 'btn-primary'}`}>Deploy rev {r.number} to {prod ? 'PROD' : 'NON-PROD'} {'—'} {profileName(r.safety_profile)}</Btn></ModalActions>
      </form>
    </Modal>
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
    <Modal title={`Update image of ${workload} / ${container}`} icon="refresh" onClose={onClose}>
      <form onSubmit={form.onSubmit} noValidate className="flex flex-col gap-2">
        <div className="flex flex-wrap gap-2"><TierBadge tier={s.environment_tier} large /> <ProfileBadge profile={s.safety_profile} large /></div>
        <TierBanner tier={s.environment_tier} />
        <Field label="New image (same repository)" vhint="An image is required"><input className="input validator w-full" required value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} /></Field>
        <Field label="Reason" vhint="A reason is required"><input className="input validator w-full" required value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        {prod && <Field label={<>Type <code>{s.namespace}</code> to confirm</>} vhint={`Type ${s.namespace} exactly`}>
          <input className="input validator w-full" required pattern={s.namespace} value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></Field>}
        <FormError msg={form.error} />
        <ModalActions><button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button><Btn type="submit" busy={form.busy} className={`btn btn-sm ${prod ? 'btn-error' : 'btn-primary'}`}>Update image{prod ? ' on PROD' : ''}</Btn></ModalActions>
      </form>
    </Modal>
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
    <div className="flex flex-col gap-5">
      <SectionCard icon="plus" title="Actions" subtitle="Create a revision, or retire this stack.">
        <div className="flex flex-wrap gap-2"><Link className="btn btn-sm btn-primary" to={`/n/${s.namespace}/s/${s.slug}/revisions/new`}><Icon name="plus" className="size-4" />New revision</Link>
          <Tip tip={!draft && !a.allowed ? 'Prod needs an active access grant.' : draft ? 'Remove this draft stack.' : 'Remove the workloads of this stack. Volumes are never deleted.'}>
            <button className="btn btn-sm btn-outline btn-error" disabled={!draft && !a.allowed} onClick={() => setDlg('teardown')}><Icon name="trash" className="size-4" />{draft ? 'Delete draft' : 'Teardown'}</button></Tip>
        </div>
      </SectionCard>
      {rows.length > 0 && <SectionCard icon="refresh" title="Container images" subtitle="Update one container's image without a new revision.">
        <ul className="list">{rows.map((r) => <li key={`${r.workload}/${r.container}`} className="list-row items-center px-0"><div className="list-col-grow min-w-0"><p className="font-medium break-words">{r.workload} / {r.container}</p><p className="text-sm font-mono break-all">{r.image}</p></div>
          <button className="btn btn-sm" disabled={!a.allowed} aria-label={`Update image of ${r.workload} ${r.container}`} onClick={() => setDlg(r)}>Update image</button></li>)}</ul>
      </SectionCard>}
      {dlg === 'teardown' && <ConfirmDialog title={draft ? 'Delete draft stack' : `Tear down ${s.slug}`} confirmLabel={draft ? 'Delete draft' : 'Tear down'}
        description={draft ? 'The draft and its revisions are removed.' : 'Workloads are removed from the cluster. Persistent volumes are never deleted.'}
        typed={s.environment_tier === 'prod' && !draft ? s.namespace : undefined} onClose={() => setDlg(null)}
        onConfirm={async (reason) => {
          const d = await act(() => send<S['Deployment']>('DELETE', '/namespaces/{ns}/stacks/{stack}', { params: { path: { ns: s.namespace, stack: s.slug } },
            body: { reason, confirm_environment_tier: s.environment_tier } }), draft ? 'Draft deleted' : 'Teardown requested', [['inventory'], ['deployments', s.namespace, s.slug]])
          if (d && (d as any).id) nav(`/deployments/${(d as any).id}`); else if (d && draft) nav(`/n/${s.namespace}`)
          return d
        }} />}
      {dlg && dlg !== 'teardown' && <ImageUpdateDialog s={s} {...dlg} current={dlg.image} onClose={() => setDlg(null)} />}
    </div>
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
