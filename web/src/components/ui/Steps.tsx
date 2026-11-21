import type { ReactNode } from 'react'
import { ACCENT, type Accent } from './StatCard'

// daisyUI `steps`. state: done = primary, current = primary + aria-current, error/warning/success colour the step, todo = neutral.
export type StepState = 'done' | 'current' | 'todo' | 'error' | 'warning' | 'success'
const STEP: Record<StepState, string> = { done: 'step-primary', current: 'step-primary', todo: '', error: 'step-error', warning: 'step-warning', success: 'step-success' }
export function Stepper({ steps, label, vertical }: { steps: { label: string; state: StepState }[]; label: string; vertical?: boolean }) {
  return (
    <ul className={`steps w-full ${vertical ? 'steps-vertical' : ''}`} aria-label={label}>
      {steps.map((s) => <li key={s.label} className={`step ${STEP[s.state]}`} aria-current={s.state === 'current' ? 'step' : undefined}>{s.label}</li>)}
    </ul>
  )
}

// daisyUI vertical, compact `timeline`: one marker per entry. Used for audit feeds, grant lifecycles, deployment history.
export type TimelineItem = { id: string | number; tone?: Accent; content: ReactNode }
export function Timeline({ items, label }: { items: TimelineItem[]; label: string }) {
  return (
    <ul className="timeline timeline-vertical timeline-compact timeline-snap-icon" aria-label={label}>
      {items.map((it, i) => <li key={it.id}>
        {i > 0 && <hr />}
        <div className="timeline-middle"><span className={`status status-lg ${ACCENT[it.tone ?? 'primary'].dot}`} aria-hidden="true" /></div>
        <div className="timeline-end timeline-box my-1 min-w-0">{it.content}</div>
        {i < items.length - 1 && <hr />}
      </li>)}
    </ul>
  )
}
