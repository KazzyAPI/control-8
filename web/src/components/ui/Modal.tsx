import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { reqErr } from '../../validate'
import { Btn } from './Buttons'
import { FormError, FormField } from './FormField'
import { Icon, type IconName } from './Icon'

// UI-030: native modal <dialog> (focus trap, Esc, focus restore). Responsive: bottom sheet on phones.
export function Modal({ title, icon, onClose, wide, children }: { title: string; icon?: IconName; onClose: () => void; wide?: boolean; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null)
  const id = useId()
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', '')
    return () => { if (typeof d.close === 'function') d.close() }
  }, [])
  return (
    <dialog ref={ref} className="modal modal-bottom sm:modal-middle" aria-labelledby={id} onCancel={(e) => { e.preventDefault(); onClose() }}>
      <div className={`modal-box ${wide ? 'sm:max-w-2xl' : ''}`}>
        <button type="button" className="btn btn-sm btn-circle btn-ghost absolute right-3 top-3" aria-label="Close dialog" onClick={onClose}><Icon name="x" className="size-4" /></button>
        <h2 id={id} className="text-lg font-bold flex items-center gap-2 pr-8">{icon && <Icon name={icon} className="size-5 text-primary" />}{title}</h2>
        <div className="mt-3 flex flex-col gap-3">{children}</div>
      </div>
      <form method="dialog" className="modal-backdrop"><button type="button" tabIndex={-1} aria-hidden="true" onClick={onClose}>close</button></form>
    </dialog>
  )
}

// Footer row shared by every dialog form: Esc hint on the left, actions on the right.
export const ModalActions = ({ children }: { children: ReactNode }) => (
  <div className="modal-action items-center mt-2">
    <span className="mr-auto text-xs text-base-content/75 max-sm:hidden">Press <kbd className="kbd kbd-xs">Esc</kbd> to cancel</span>{children}
  </div>
)

// True when the typed text equals the required phrase exactly.
export const typedOk = (typed: string | undefined, input: string) => !typed || input === typed

// Confirm flow for destructive or prod actions: optional required reason, optional typed confirmation (daisyUI `validator`).
export function ConfirmDialog({ title, icon = 'warning', description, confirmLabel, tone = 'error', typed, reason = true, onClose, onConfirm, children }: {
  title: string; icon?: IconName; description?: ReactNode; confirmLabel: string; tone?: 'error' | 'primary' | 'warning'; typed?: string; reason?: boolean
  onClose: () => void; onConfirm: (reason: string) => Promise<unknown>; children?: ReactNode
}) {
  const [text, setText] = useState('')
  const [t, setT] = useState('')
  const [shown, setShown] = useState(false)
  const [busy, setBusy] = useState(false)
  const err = (reason ? reqErr(text, 'Reason') : null) ?? (typedOk(typed, t) ? null : `Type "${typed}" to confirm`)
  const go = async (e: FormEvent) => {
    e.preventDefault(); setShown(true)
    if (err) return
    setBusy(true)
    try { if ((await onConfirm(text.trim())) !== undefined) onClose() } finally { setBusy(false) }
  }
  const btn = tone === 'error' ? 'btn-error' : tone === 'warning' ? 'btn-warning' : 'btn-primary'
  return (
    <Modal title={title} icon={icon} onClose={onClose}>
      <form onSubmit={go} noValidate className="flex flex-col gap-2">
        {description && <p className="text-sm">{description}</p>}
        {children}
        {reason && <FormField label="Reason" vhint="A reason is required"><input className="input validator w-full" required value={text} onChange={(e) => setText(e.target.value)} autoFocus /></FormField>}
        {typed && <FormField label={<>Type <code>{typed}</code> to confirm</>} vhint={`Type ${typed} exactly`}>
          <input className="input validator w-full" required pattern={typed.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} value={t} onChange={(e) => setT(e.target.value)} autoComplete="off" /></FormField>}
        <FormError msg={shown ? err : null} />
        <ModalActions><button type="button" className="btn btn-sm" onClick={onClose}>Cancel</button><Btn type="submit" busy={busy} className={`btn btn-sm ${btn}`}>{confirmLabel}</Btn></ModalActions>
      </form>
    </Modal>
  )
}
