import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

export type NoticeTone = 'info' | 'success' | 'warning' | 'error'
// Calm banner: a neutral daisyUI `alert` surface with a coloured accent edge and icon, so text keeps full contrast in every theme.
const TONE: Record<NoticeTone, { edge: string; icon: string; name: IconName }> = {
  info: { edge: 'border-l-info', icon: 'text-info', name: 'info' },
  success: { edge: 'border-l-success', icon: 'text-success', name: 'check-circle' },
  warning: { edge: 'border-l-warning', icon: 'text-warning', name: 'warning' },
  error: { edge: 'border-l-error', icon: 'text-error', name: 'x-circle' },
}

// `role` defaults to status; pass "alert" only for something the user must hear immediately (form errors, failed actions).
export function Notice({ tone = 'info', title, action, onDismiss, role = 'status', className = '', children }: {
  tone?: NoticeTone; title?: ReactNode; action?: ReactNode; onDismiss?: () => void; role?: 'status' | 'alert' | 'none'; className?: string; children?: ReactNode
}) {
  const t = TONE[tone]
  return (
    <div role={role === 'none' ? undefined : role} className={`alert border-l-4 ${t.edge} ${className}`}>
      <Icon name={t.name} className={`size-5 ${t.icon}`} />
      <div className="flex-1 min-w-0 text-sm">{title && <p className="font-semibold">{title}</p>}{children}</div>
      {action && <div className="flex flex-wrap gap-2">{action}</div>}
      {onDismiss && <button type="button" className="btn btn-ghost btn-xs btn-circle" aria-label="Dismiss" onClick={onDismiss}><Icon name="x" className="size-4" /></button>}
    </div>
  )
}

// Slim production banner: text says PRODUCTION (never colour alone).
export const TierBanner = ({ tier }: { tier?: string | null }) => tier === 'prod'
  ? <Notice tone="error" className="py-2"><strong>Production environment.</strong> Changes here affect live systems and need an access grant.</Notice> : null
