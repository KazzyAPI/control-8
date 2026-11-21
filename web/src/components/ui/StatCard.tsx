import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Icon, type IconName } from './Icon'
import { Radial } from './Time'

export type Accent = 'primary' | 'secondary' | 'accent' | 'success' | 'warning' | 'error' | 'info'
// Full class names so Tailwind generates them.
export const ACCENT: Record<Accent, { bar: string; chip: string; text: string; dot: string }> = {
  primary: { bar: 'border-t-primary', chip: 'bg-primary/15 text-primary', text: 'text-primary', dot: 'status-primary' },
  secondary: { bar: 'border-t-secondary', chip: 'bg-secondary/15 text-secondary', text: 'text-secondary', dot: 'status-secondary' },
  accent: { bar: 'border-t-accent', chip: 'bg-accent/15 text-accent', text: 'text-accent', dot: 'status-accent' },
  success: { bar: 'border-t-success', chip: 'bg-success/15 text-success', text: 'text-success', dot: 'status-success' },
  warning: { bar: 'border-t-warning', chip: 'bg-warning/20 text-warning', text: 'text-warning', dot: 'status-warning' },
  error: { bar: 'border-t-error', chip: 'bg-error/15 text-error', text: 'text-error', dot: 'status-error' },
  info: { bar: 'border-t-info', chip: 'bg-info/15 text-info', text: 'text-info', dot: 'status-info' },
}

// One daisyUI `stat` as its own tile: accent top edge, icon chip (or a radial-progress when `progress` is given), value, description.
// Title and description are forced to full contrast (daisyUI dims them to 60%).
export function StatCard({ title, value, desc, icon, accent = 'primary', progress, to }: {
  title: string; value: ReactNode; desc?: ReactNode; icon?: IconName; accent?: Accent; progress?: number; to?: string
}) {
  const a = ACCENT[accent]
  return (
    <div className={`stats bg-base-100 border border-base-300 border-t-4 ${a.bar} shadow-sm w-full overflow-hidden`}>
      <div className="stat px-5 py-4">
        {(icon || progress !== undefined) && <div className="stat-figure">
          {progress !== undefined ? <Radial value={progress} label={`${title}: ${Math.round(progress)} percent`} tone={accent === 'secondary' || accent === 'accent' ? 'primary' : accent} />
            : <span className={`grid place-items-center size-11 rounded-full ${a.chip}`}><Icon name={icon!} className="size-6" /></span>}
        </div>}
        <div className="stat-title text-base-content font-medium">{to ? <Link to={to}>{title}</Link> : title}</div>
        <div className="stat-value text-3xl">{value}</div>
        {desc && <div className="stat-desc text-base-content whitespace-normal">{desc}</div>}
      </div>
    </div>
  )
}

// Responsive row of tiles.
export const StatGrid = ({ children }: { children: ReactNode }) => <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 xl:grid-cols-4">{children}</div>
