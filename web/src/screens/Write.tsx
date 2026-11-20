import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { ProfileBadge, TierBadge } from '../components/Badges'
import { useGet, useProdAccess, useTitle, type S } from '../lib'
import { ApiError, Dialog, FormError, ReasonDialog, send, useAct } from '../ui'
import { deployErr, grantErr, lines, reqErr, revisionErr, slugErr } from '../validate'

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
    <form onSubmit={form.onSubmit} noValidate aria-label="Create asset" className="stackform">
      <h3>Create asset</h3>
      <label>Slug <input value={f.slug} onChange={(e) => setF({ ...f, slug: e.target.value })} /></label>
      <label>Name <input value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></label>
      <label>Managed by <select value={f.managed_by} onChange={(e) => setF({ ...f, managed_by: e.target.value })}><option value="control8">Control 8</option><option value="argo">Argo</option></select></label>
      <label>Image repository (optional) <input value={f.image_repo} onChange={(e) => setF({ ...f, image_repo: e.target.value })} /></label>
      <FormError msg={form.error} />
      <button type="submit" disabled={form.busy}>Create asset</button>
    </form>
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
    <form onSubmit={form.onSubmit} noValidate aria-label="Create stack" className="stackform">
      <h3>Create stack</h3>
      <label>Slug <input value={f.slug} onChange={(e) => setF({ ...f, slug: e.target.value })} /></label>
      <label>Asset <select value={f.asset} onChange={(e) => setF({ ...f, asset: e.target.value })}><option value="">Choose…</option>
        {(assets.data?.items ?? []).map((a) => <option key={a.slug} value={a.slug}>{a.name}</option>)}</select></label>
      <label>Target <select value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })}><option value="">Choose…</option>
        {(targets.data?.items ?? []).map((t) => <option key={t.name} value={t.name}>{t.name} ({t.environment_tier})</option>)}</select></label>
      <FormError msg={form.error} />
      <button type="submit" disabled={form.busy}>Create stack</button>
    </form>
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
    <form onSubmit={form.onSubmit} noValidate aria-label="Approver group" className="stackform">
      <label>Approver group <input value={v} onChange={(e) => setV(e.target.value)} /></label>
      <FormError msg={form.error} />
      <button type="submit" disabled={form.busy}>Save approver group</button>
    </form>
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
      <h1>New revision</h1>
      <p><Link to={`/n/${ns}/s/${stack}`}>Back to {stack}</Link></p>
      <form onSubmit={form.onSubmit} noValidate aria-label="New revision" className="stackform">
        <label>Safety profile <select value={f.profile} onChange={(e) => setF({ ...f, profile: e.target.value })}>
          <option value="">Choose…</option><option value="managed_rollout">Managed rollout</option><option value="as_is">As-is — no safety net</option></select></label>
        <label>Manifests (YAML) <textarea rows={14} value={f.manifests} onChange={(e) => setF({ ...f, manifests: e.target.value })} spellCheck={false} /></label>
        <label>Rollout timeout in seconds (optional) <input inputMode="numeric" value={f.timeout} onChange={(e) => setF({ ...f, timeout: e.target.value })} /></label>
        <label><input type="checkbox" checked={f.digest_poll} onChange={(e) => setF({ ...f, digest_poll: e.target.checked })} /> Follow image digests (managed rollout only)</label>
        <label>Database workloads (comma separated, optional) <input value={f.dbs} onChange={(e) => setF({ ...f, dbs: e.target.value })} /></label>
        <FormError msg={form.error} />
        {violations.length > 0 && <div role="alert"><p>Policy violations:</p><ul>{violations.map((v, i) => <li key={i}>{v.rule}{v.service && ` (${v.service})`}: {v.message}</li>)}</ul></div>}
        <button type="submit" disabled={form.busy}>Create revision</button>
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
        <p><TierBadge tier={s.environment_tier} /> <ProfileBadge profile={r.safety_profile} large /></p>
        {changed && <p className="red-line">Safety profile changes: {profileName(s.safety_profile)} {'→'} {profileName(r.safety_profile)}</p>}
        <fieldset><legend>Safety profile of this revision</legend>
          {(['managed_rollout', 'as_is'] as const).map((p) => <label key={p}><input type="radio" name="profile" checked={f.picked === p} onChange={() => setF({ ...f, picked: p })} /> {profileName(p)}</label>)}
        </fieldset>
        {r.safety_profile === 'as_is' && <label><input type="checkbox" checked={f.ack} onChange={(e) => setF({ ...f, ack: e.target.checked })} /> I understand there is no automatic rollback</label>}
        {prod && <label>Type <code>{s.namespace}</code> to confirm this production deploy <input value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></label>}
        <label>Reason (optional) <input value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></label>
        <FormError msg={form.error} />
        <p><button type="submit" disabled={form.busy}>Deploy rev {r.number} to {prod ? 'PROD' : 'NON-PROD'} {'—'} {profileName(r.safety_profile)}</button> <button type="button" onClick={onClose}>Cancel</button></p>
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
        <p><TierBadge tier={s.environment_tier} /> <ProfileBadge profile={s.safety_profile} large /></p>
        <label>New image (same repository) <input value={f.image} onChange={(e) => setF({ ...f, image: e.target.value })} /></label>
        <label>Reason <input value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></label>
        {prod && <label>Type <code>{s.namespace}</code> to confirm <input value={f.typed} onChange={(e) => setF({ ...f, typed: e.target.value })} autoComplete="off" /></label>}
        <FormError msg={form.error} />
        <p><button type="submit" disabled={form.busy}>Update image{prod ? ' on PROD' : ''}</button> <button type="button" onClick={onClose}>Cancel</button></p>
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
      <h2 id="h-act">Actions</h2>
      <p><Link className="button" to={`/n/${s.namespace}/s/${s.slug}/revisions/new`}>New revision</Link>{' '}
        <button disabled={!draft && !a.allowed} onClick={() => setDlg('teardown')}>{draft ? 'Delete draft' : 'Teardown'}</button>
        </p>
      {rows.length > 0 && <ul>{rows.map((r) => <li key={`${r.workload}/${r.container}`}>{r.workload} / {r.container}: {r.image}{' '}
        <button disabled={!a.allowed} aria-label={`Update image of ${r.workload} ${r.container}`} onClick={() => setDlg(r)}>Update image</button></li>)}</ul>}
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
      <h1>Request access</h1>
      <form onSubmit={form.onSubmit} noValidate aria-label="Request prod access" className="stackform">
        <label>Namespace <select value={f.namespace} onChange={(e) => setF({ ...f, namespace: e.target.value })}><option value="">Choose…</option>
          {(nss.data?.items ?? []).map((n) => <option key={n.slug} value={n.slug}>{n.slug}</option>)}</select></label>
        <label>Target <select value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })}><option value="">Choose…</option>
          {(targets.data?.items ?? []).map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}</select></label>
        <label>Reason (10 to 500 characters) <textarea rows={3} value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></label>
        <label>Ticket reference (optional) <input value={f.ticket_ref} onChange={(e) => setF({ ...f, ticket_ref: e.target.value })} /></label>
        <label>Duration in minutes <input inputMode="numeric" value={f.duration} onChange={(e) => setF({ ...f, duration: e.target.value })} /></label>
        <label>Start (optional, within 7 days) <input type="datetime-local" value={f.starts_at} onChange={(e) => setF({ ...f, starts_at: e.target.value })} /></label>
        <FormError msg={form.error} />
        <button type="submit" disabled={form.busy}>Request access</button>
      </form>
    </>
  )
}
