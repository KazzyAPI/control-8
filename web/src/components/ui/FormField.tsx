import { cloneElement, useId, type ReactElement, type ReactNode } from 'react'
import { Notice } from './Notice'

// daisyUI v5 form row: fieldset + legend-styled label wired to the control by id; `vhint` is the validator-hint
// (shown by the control's `validator` state), `hint` is always-visible help text.
export function FormField({ label, hint, vhint, children }: { label: ReactNode; hint?: ReactNode; vhint?: string; children: ReactElement<{ id?: string }> }) {
  const id = useId()
  return (
    <fieldset className="fieldset">
      <label className="fieldset-legend" htmlFor={id}>{label}</label>
      {cloneElement(children, { id })}
      {vhint && <p className="validator-hint">{vhint}</p>}
      {hint && <p className="label text-base-content/75 whitespace-normal">{hint}</p>}
    </fieldset>
  )
}

// Inline form error (the only role=alert in a form).
export const FormError = ({ msg }: { msg: string | null }) => (msg ? <Notice tone="error" role="alert">{msg}</Notice> : null)

// A labelled switch row (daisyUI `toggle`) with a one-line explanation under it.
export function ToggleRow({ label, hint, checked, onChange }: { label: ReactNode; hint?: ReactNode; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-start gap-3 cursor-pointer py-1">
      <input type="checkbox" className="toggle toggle-sm toggle-primary mt-0.5" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span><span className="font-medium">{label}</span>{hint && <span className="block text-sm text-base-content/75">{hint}</span>}</span>
    </label>
  )
}
