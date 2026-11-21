import { useId, type KeyboardEvent, type ReactNode } from 'react'
import { Icon, type IconName } from './Icon'

// daisyUI `tabs` (border style); button tabs + roving tabindex + arrow keys. `tabs` entries are [key, label, optional icon].
export function Tabs<T extends string>({ tabs, value, onChange, label, children }: { tabs: [T, string, IconName?][]; value: T; onChange: (t: T) => void; label: string; children: ReactNode }) {
  const id = useId()
  const move = (e: KeyboardEvent, i: number) => {
    const j = e.key === 'ArrowRight' ? (i + 1) % tabs.length : e.key === 'ArrowLeft' ? (i + tabs.length - 1) % tabs.length : e.key === 'Home' ? 0 : e.key === 'End' ? tabs.length - 1 : -1
    if (j < 0) return
    e.preventDefault(); onChange(tabs[j][0]); document.getElementById(`${id}-t-${tabs[j][0]}`)?.focus()
  }
  return <div className="flex flex-col gap-4">
    <div role="tablist" aria-label={label} className="tabs tabs-border overflow-x-auto flex-nowrap border-b border-base-300">
      {tabs.map(([k, text, icon], i) => <button key={k} id={`${id}-t-${k}`} role="tab" type="button" aria-selected={value === k} aria-controls={`${id}-p`} tabIndex={value === k ? 0 : -1}
        className={`tab gap-2 whitespace-nowrap text-base-content ${value === k ? 'tab-active font-semibold' : ''}`} onClick={() => onChange(k)} onKeyDown={(e) => move(e, i)}>
        {icon && <Icon name={icon} className="size-4" />}{text}</button>)}
    </div>
    <div role="tabpanel" id={`${id}-p`} aria-labelledby={`${id}-t-${value}`} className="flex flex-col gap-5">{children}</div>
  </div>
}
