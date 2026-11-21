import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

// Every screen starts with this: optional breadcrumb slot, icon, h1 title, subtitle, tier/safety badges and primary actions.
// The h1 holds only the title text (screens and tests find the page by it).
export function PageHeader({ title, subtitle, icon, breadcrumbs, badges, actions }: {
  title: ReactNode; subtitle?: ReactNode; icon?: IconName; breadcrumbs?: ReactNode; badges?: ReactNode; actions?: ReactNode
}) {
  return (
    <header className="flex flex-col gap-2">
      {breadcrumbs}
      <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
        {icon && <span className="hidden sm:grid place-items-center size-12 rounded-box bg-primary/10 text-primary"><Icon name={icon} className="size-6" /></span>}
        <div className="min-w-0 flex-1">
          <h1 className="text-2xl font-bold break-words leading-tight">{title}</h1>
          {subtitle && <div className="text-sm text-base-content/75 mt-1">{subtitle}</div>}
          {badges && <div className="flex flex-wrap items-center gap-2 mt-2">{badges}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </header>
  )
}

// Standard page body: consistent vertical rhythm between header, banners and cards.
export const Page = ({ children }: { children: ReactNode }) => <div className="flex flex-col gap-5">{children}</div>
