import type { CSSProperties, ReactNode } from 'react'

// Time widgets. `--value` is set through React's style prop, which uses the CSSOM (allowed by the strict CSP; no style attributes in HTML).
export function splitDuration(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000))
  return { h: Math.min(Math.floor(s / 3600), 99), m: Math.floor((s % 3600) / 60), s: s % 60 }
}
// `--digits:2` is what makes daisyUI reserve and zero-pad two digits (without it the tens column is clipped and the number garbles).
const cv = (n: number) => ({ '--value': n, '--digits': 2 } as CSSProperties)

// h:m:s countdown (daisyUI `countdown`, one span per unit). The timer role carries the full reading; the digits are decorative.
export function Countdown({ ms, className = 'text-lg' }: { ms: number; className?: string }) {
  const { h, m, s } = splitDuration(ms)
  return (
    <span className={`inline-flex items-center gap-0.5 font-mono ${className}`} role="timer" aria-label={`${h} hours ${m} minutes ${s} seconds left`}>
      <span className="countdown" aria-hidden="true"><span style={cv(h)}>{h}</span></span>:
      <span className="countdown" aria-hidden="true"><span style={cv(m)}>{m}</span></span>:
      <span className="countdown" aria-hidden="true"><span style={cv(s)}>{s}</span></span>
    </span>
  )
}

const TONE = { primary: 'text-primary', success: 'text-success', warning: 'text-warning', error: 'text-error', info: 'text-info', secondary: 'text-secondary' }
export const Radial = ({ value, label, tone = 'primary', size = '3.5rem', children }: { value: number; label: string; tone?: keyof typeof TONE; size?: string; children?: ReactNode }) => {
  const v = Math.max(0, Math.min(100, Math.round(value)))
  return <div className={`radial-progress ${TONE[tone]} text-xs font-semibold`} style={{ '--value': v, '--size': size, '--thickness': '5px' } as CSSProperties} role="progressbar" aria-label={label} aria-valuenow={v} aria-valuemin={0} aria-valuemax={100}>
    <span className="text-base-content">{children ?? `${v}%`}</span></div>
}
