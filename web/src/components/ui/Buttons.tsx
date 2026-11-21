import { useState, type ButtonHTMLAttributes } from 'react'

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
