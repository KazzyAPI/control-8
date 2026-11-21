import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

// A titled daisyUI `card`; with `collapsible` it is a daisyUI `collapse` instead. `flush` removes body padding so a table runs edge to edge.
export function SectionCard({ title, icon, subtitle, actions, collapsible, defaultOpen = true, flush, className = '', children }: {
  title: ReactNode; icon?: IconName; subtitle?: ReactNode; actions?: ReactNode; collapsible?: boolean; defaultOpen?: boolean; flush?: boolean; className?: string; children: ReactNode
}) {
  const head = (
    <>
      {icon && <Icon name={icon} className="size-5 text-primary" />}
      <div className="min-w-0"><h2 className="card-title text-base">{title}</h2>{subtitle && <p className="text-sm text-base-content/75">{subtitle}</p>}</div>
    </>
  )
  if (collapsible) return (
    <details className={`collapse collapse-arrow bg-base-100 border border-base-300 shadow-sm ${className}`} open={defaultOpen}>
      <summary className="collapse-title flex items-center gap-2 font-semibold">{head}</summary>
      <div className="collapse-content">{children}</div>
    </details>
  )
  return (
    <section className={`card card-border bg-base-100 shadow-sm min-w-0 ${className}`}>
      <div className={`card-body gap-4 ${flush ? 'p-0' : 'p-5'}`}>
        <div className={`flex flex-wrap items-center gap-x-3 gap-y-2 ${flush ? 'px-5 pt-5' : ''}`}>{head}{actions && <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>}</div>
        {children}
      </div>
    </section>
  )
}
