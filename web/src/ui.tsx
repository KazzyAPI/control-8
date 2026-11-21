import { useQueryClient } from '@tanstack/react-query'
import { cloneElement, createContext, useCallback, useContext, useEffect, useId, useRef, useState, type ButtonHTMLAttributes, type FormEvent, type ReactElement, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { api } from './api/client'
import type { S } from './lib'
import { reqErr } from './validate'

// API errors are problem+json (API-003); the code and title surface as toasts.
export class ApiError extends Error { constructor(public problem: Partial<S['Problem']>) { super(problem.detail || problem.title || `HTTP ${problem.status}`) } }
export async function send<T = any>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, opts: { params?: object; body?: unknown } = {}): Promise<T> {
  const { data, error, response } = await (api as any)[method](path, opts)
  if (!response.ok) throw new ApiError(error && typeof error === 'object' ? { status: response.status, ...error } : { status: response.status, title: response.statusText || 'Request failed', code: 'http_error' })
  return (data ?? {}) as T // 204 has no body; callers test for undefined to detect failure
}

type Toast = { id: number; text: string; kind: 'ok' | 'error' }
const Ctx = createContext<{ toast: (text: string, kind?: Toast['kind']) => void; say: (text: string) => void }>({ toast() {}, say() {} })
export const useToast = () => useContext(Ctx)

// UI-031: one polite live region, at most one announcement per 5 s. Error toasts are role=alert.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const [said, setSaid] = useState('')
  const last = useRef(0)
  const toast = useCallback((text: string, kind: Toast['kind'] = 'ok') => {
    const id = Date.now() + Math.random()
    setToasts((t) => [...t, { id, text, kind }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 8000)
  }, [])
  const say = useCallback((text: string) => { if (Date.now() - last.current >= 5000) { last.current = Date.now(); setSaid(text) } }, [])
  return (
    <Ctx.Provider value={{ toast, say }}>
      {children}
      <div className="toast toast-end z-50 max-lg:mb-16">
        {toasts.map((t) => <div key={t.id} className={`alert ${t.kind === 'error' ? 'alert-error' : 'alert-success'}`} role={t.kind === 'error' ? 'alert' : 'status'}>{t.text}</div>)}
      </div>
      <div className="sr-only" role="status" aria-live="polite">{said}</div>
    </Ctx.Provider>
  )
}

// Runs a mutation: errors become toasts (grant_required also opens the request form), success invalidates `inv` keys.
export function useAct() {
  const { toast } = useToast()
  const nav = useNavigate()
  const qc = useQueryClient()
  return useCallback(async <T,>(fn: () => Promise<T>, ok?: string, inv: unknown[][] = []): Promise<T | undefined> => {
    try {
      const r = await fn()
      if (ok) toast(ok)
      for (const queryKey of inv) qc.invalidateQueries({ queryKey })
      return r
    } catch (e) {
      const p = e instanceof ApiError ? e.problem : { title: String(e) }
      toast(`${p.title ?? 'Error'}${p.detail ? `: ${p.detail}` : ''}${p.code ? ` (${p.code})` : ''}`, 'error')
      if (p.code === 'grant_required' && p.scope) nav(`/access/request?namespace=${p.scope.namespace ?? ''}&target=${p.scope.target ?? ''}`)
      return undefined
    }
  }, [toast, nav, qc])
}

// UI-030: native modal dialog (focus trap, Esc, focus restore).
export function Dialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', '')
    return () => { if (typeof d.close === 'function') d.close() }
  }, [])
  return (
    <dialog ref={ref} className="modal modal-bottom sm:modal-middle" aria-labelledby="dlg-title" onCancel={(e) => { e.preventDefault(); onClose() }}>
      <div className="modal-box">
        <h2 id="dlg-title" className="text-lg font-bold mt-0">{title}</h2>
        {children}
        <p className="text-xs mt-2">Press <kbd className="kbd kbd-xs">Esc</kbd> to cancel.</p>
      </div>
      <form method="dialog" className="modal-backdrop"><button type="button" tabIndex={-1} aria-hidden="true" onClick={onClose}>close</button></form>
    </dialog>
  )
}

export const FormError = ({ msg }: { msg: string | null }) => (msg ? <div className="alert alert-error my-2" role="alert"><span>{msg}</span></div> : null)

// daisyUI v5 form row: fieldset + legend-styled label wired to the control by id; `vhint` is the validator-hint
// (shown by the control's `validator` state), `hint` is always-visible help.
export function Field({ label, hint, vhint, children }: { label: ReactNode; hint?: ReactNode; vhint?: string; children: ReactElement<{ id?: string }> }) {
  const id = useId()
  return (
    <fieldset className="fieldset">
      <label className="fieldset-legend" htmlFor={id}>{label}</label>
      {cloneElement(children, { id })}
      {vhint && <p className="validator-hint">{vhint}</p>}
      {hint && <p className="text-sm">{hint}</p>}
    </fieldset>
  )
}

// Button with a spinner while its mutation runs (disabled meanwhile). The spinner is decorative; the name stays the label.
export function Btn({ busy, children, className = 'btn btn-sm', ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { busy?: boolean }) {
  return <button {...p} className={className} disabled={busy || p.disabled} aria-busy={busy || undefined}>
    {busy && <span className="loading loading-spinner loading-xs" aria-hidden="true" />}{children}</button>
}
// Click handler that may return a promise: busy until it settles.
export function AsyncBtn({ onClick, ...p }: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onClick'> & { onClick: () => unknown }) {
  const [busy, setBusy] = useState(false)
  return <Btn {...p} busy={busy} onClick={async () => { setBusy(true); try { await onClick() } finally { setBusy(false) } }} />
}

// Reason (+ optional typed confirmation) dialog used by revoke, reject, teardown.
export function ReasonDialog({ title, submit, typed, onClose, onSubmit, children }: {
  title: string; submit: string; typed?: string; onClose: () => void; onSubmit: (reason: string) => Promise<unknown>; children?: ReactNode
}) {
  const [reason, setReason] = useState('')
  const [t, setT] = useState('')
  const [shown, setShown] = useState(false)
  const [busy, setBusy] = useState(false)
  const err = reqErr(reason, 'Reason') ?? (typed && t !== typed ? `Type "${typed}" to confirm` : null)
  const go = async (e: FormEvent) => {
    e.preventDefault(); setShown(true)
    if (err) return
    setBusy(true)
    try { if ((await onSubmit(reason.trim())) !== undefined) onClose() } finally { setBusy(false) }
  }
  return (
    <Dialog title={title} onClose={onClose}>
      <form onSubmit={go} noValidate>
        {children}
        <Field label="Reason" vhint="A reason is required"><input className="input validator w-full" required value={reason} onChange={(e) => setReason(e.target.value)} autoFocus /></Field>
        {typed && <Field label={<>Type <code>{typed}</code> to confirm</>} vhint={`Type ${typed} exactly`}>
          <input className="input validator w-full" required pattern={typed} value={t} onChange={(e) => setT(e.target.value)} autoComplete="off" /></Field>}
        <FormError msg={shown ? err : null} />
        <div className="modal-action"><Btn type="submit" busy={busy} className="btn btn-error btn-sm">{submit}</Btn> <button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button></div>
      </form>
    </Dialog>
  )
}
