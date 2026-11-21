import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Icon, type IconName } from './Icon'

// Empty state: daisyUI `hero` with an illustrated icon, one line of text, an optional hint and a primary call to action.
export function EmptyState({ children, hint, icon = 'inbox', to, cta, onCta }: { children: ReactNode; hint?: ReactNode; icon?: IconName; to?: string; cta?: string; onCta?: () => void }) {
  return (
    <div className="hero rounded-box border border-dashed border-base-300 bg-base-200/50 py-8">
      <div className="hero-content flex-col gap-2 text-center">
        <span className="relative grid place-items-center size-16 rounded-full bg-base-100 text-primary shadow-sm ring-4 ring-primary/10">
          <svg viewBox="0 0 64 64" className="absolute inset-0 size-16 text-primary/30" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="3 5" aria-hidden="true" focusable="false"><circle cx="32" cy="32" r="30" /></svg>
          <Icon name={icon} className="size-7" />
        </span>
        <p className="font-semibold">{children}</p>
        {hint && <p className="text-sm text-base-content/75 max-w-sm">{hint}</p>}
        {cta && (to ? <Link className="btn btn-primary btn-sm mt-1" to={to}><Icon name="plus" className="size-4" />{cta}</Link> : <button type="button" className="btn btn-primary btn-sm mt-1" onClick={onCta}><Icon name="plus" className="size-4" />{cta}</button>)}
      </div>
    </div>
  )
}
