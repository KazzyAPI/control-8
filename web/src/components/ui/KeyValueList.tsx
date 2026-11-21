import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

// Definition grid for facts about one thing (owner, target, revision...). Values are React children (text stays text, UI-003).
export function KeyValueList({ items, cols = 1 }: { items: { label: string; value: ReactNode; icon?: IconName }[]; cols?: 1 | 2 | 3 }) {
  const grid = cols === 3 ? 'sm:grid-cols-2 lg:grid-cols-3' : cols === 2 ? 'sm:grid-cols-2' : ''
  return (
    <dl className={`grid gap-x-8 gap-y-3 text-sm ${grid}`}>
      {items.map((i) => <div key={i.label} className="min-w-0"><dt className="flex items-center gap-1.5 text-base-content/75 text-xs uppercase tracking-wide">{i.icon && <Icon name={i.icon} className="size-4" />}{i.label}</dt><dd className="font-medium break-words">{i.value === "" || i.value == null ? "—" : i.value}</dd></div>)}
    </dl>
  )
}
