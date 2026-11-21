import { useId, type CSSProperties, type KeyboardEvent, type ReactNode } from 'react'
import { Link } from 'react-router'

// Presentational daisyUI building blocks shared by every screen. No data fetching here.
// All dynamic strings are rendered as React text (UI-003).

// ---- skeletons: shaped like the content they stand in for; one polite "Loading" status each -------------------
const Loading = () => <span className="sr-only">Loading{'…'}</span>
export const SkTable = ({ rows = 4, cols = 5 }: { rows?: number; cols?: number }) => (
  <div role="status" className="my-2">
    <Loading />
    <div aria-hidden="true" className="overflow-x-auto rounded-box border border-base-300">
      <table className="table"><thead><tr>{Array.from({ length: cols }, (_, i) => <th key={i}><div className="skeleton h-4 w-20" /></th>)}</tr></thead>
        <tbody>{Array.from({ length: rows }, (_, r) => <tr key={r}>{Array.from({ length: cols }, (_, c) => <td key={c}><div className={`skeleton h-4 ${c === 0 ? 'w-32' : 'w-16'}`} /></td>)}</tr>)}</tbody></table>
    </div>
  </div>
)
export const SkStats = ({ n = 3 }: { n?: number }) => (
  <div role="status" className="my-2"><Loading />
    <div aria-hidden="true" className="stats stats-vertical sm:stats-horizontal border border-base-300 w-full">
      {Array.from({ length: n }, (_, i) => <div key={i} className="stat"><div className="skeleton h-3 w-20 mb-2" /><div className="skeleton h-8 w-16 mb-2" /><div className="skeleton h-3 w-28" /></div>)}
    </div></div>
)
export const SkCards = ({ n = 3 }: { n?: number }) => (
  <div role="status" className="my-2"><Loading />
    <div aria-hidden="true" className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {Array.from({ length: n }, (_, i) => <div key={i} className="card card-border bg-base-100"><div className="card-body gap-3">
        <div className="skeleton h-5 w-32" /><div className="skeleton h-4 w-full" /><div className="skeleton h-4 w-2/3" /></div></div>)}
    </div></div>
)
export const SkText = ({ lines = 3 }: { lines?: number }) => (
  <div role="status" className="my-2 flex flex-col gap-2"><Loading />
    {Array.from({ length: lines }, (_, i) => <div key={i} aria-hidden="true" className={`skeleton h-4 ${i === lines - 1 ? 'w-2/3' : 'w-full'}`} />)}</div>
)
export const SkPage = () => (
  <div role="status" className="my-2"><Loading />
    <div aria-hidden="true" className="flex flex-col gap-4">
      <div className="skeleton h-8 w-64" /><div className="flex gap-2"><div className="skeleton h-6 w-20" /><div className="skeleton h-6 w-32" /></div>
      <div className="stats stats-vertical sm:stats-horizontal border border-base-300">{[0, 1, 2].map((i) => <div key={i} className="stat"><div className="skeleton h-3 w-20 mb-2" /><div className="skeleton h-8 w-16" /></div>)}</div>
      <div className="skeleton h-40 w-full" />
    </div></div>
)
export type Fallback = 'table' | 'cards' | 'stats' | 'text' | 'page'
export const Skeleton = ({ kind }: { kind: Fallback }) =>
  kind === 'table' ? <SkTable /> : kind === 'cards' ? <SkCards /> : kind === 'stats' ? <SkStats /> : kind === 'page' ? <SkPage /> : <SkText />

// ---- stat (title/desc forced to full contrast: daisyUI dims them to 60%) ----------------------------------------------------
export const Stat = ({ title, value, desc, figure }: { title: string; value: ReactNode; desc?: ReactNode; figure?: ReactNode }) => (
  <div className="stat">{figure && <div className="stat-figure">{figure}</div>}<div className="stat-title text-base-content">{title}</div>
    <div className="stat-value">{value}</div>{desc && <div className="stat-desc text-base-content whitespace-normal">{desc}</div>}</div>
)
export const Stats = ({ children }: { children: ReactNode }) => <div className="stats stats-vertical sm:stats-horizontal border border-base-300 w-full my-2 overflow-x-auto">{children}</div>

// ---- empty state: hero + card with a clear next step ----------------------------------------------------------
export function Empty({ children, to, cta, onCta }: { children: ReactNode; to?: string; cta?: string; onCta?: () => void }) {
  return (
    <div className="hero bg-base-200 rounded-box my-2"><div className="hero-content text-center"><div className="card card-border bg-base-100"><div className="card-body items-center">
      <p className="my-0">{children}</p>
      {cta && <div className="card-actions">{to ? <Link className="btn btn-primary btn-sm" to={to}>{cta}</Link> : <button className="btn btn-primary btn-sm" onClick={onCta}>{cta}</button>}</div>}
    </div></div></div></div>
  )
}

// ---- header with prominent badges, and the prod ribbon ---------------------------------------------------------
export const PageHeader = ({ title, badges, actions }: { title: ReactNode; badges?: ReactNode; actions?: ReactNode }) => (
  <div className="flex flex-wrap items-center gap-2 mb-4">
    <h1 className="mb-0 mr-2">{title}</h1>{badges}
    {actions && <div className="ml-auto flex flex-wrap gap-2">{actions}</div>}
  </div>
)
// Coloured top strip on every prod page; text always says PRODUCTION (never colour alone).
export const ProdRibbon = ({ tier }: { tier?: string | null }) => tier === 'prod'
  ? <div className="alert alert-error mb-4"><strong>Production environment</strong><span>Changes here affect live systems.</span></div> : null

// ---- tabs (daisyUI tabs-lift; button tabs + roving tabindex + arrow keys) ----------------------------------------
export function Tabs<T extends string>({ tabs, value, onChange, label, children }: { tabs: [T, string][]; value: T; onChange: (t: T) => void; label: string; children: ReactNode }) {
  const id = useId()
  const move = (e: KeyboardEvent, i: number) => {
    const j = e.key === 'ArrowRight' ? (i + 1) % tabs.length : e.key === 'ArrowLeft' ? (i + tabs.length - 1) % tabs.length : e.key === 'Home' ? 0 : e.key === 'End' ? tabs.length - 1 : -1
    if (j < 0) return
    e.preventDefault(); onChange(tabs[j][0]); document.getElementById(`${id}-t-${tabs[j][0]}`)?.focus()
  }
  return <>
    <div role="tablist" aria-label={label} className="tabs tabs-lift overflow-x-auto">
      {tabs.map(([k, text], i) => <button key={k} id={`${id}-t-${k}`} role="tab" type="button" aria-selected={value === k} aria-controls={`${id}-p`} tabIndex={value === k ? 0 : -1}
        className={`tab text-base-content ${value === k ? 'tab-active' : ''}`} onClick={() => onChange(k)} onKeyDown={(e) => move(e, i)}>{text}</button>)}
    </div>
    <div role="tabpanel" id={`${id}-p`} aria-labelledby={`${id}-t-${value}`} className="bg-base-100 border-base-300 border rounded-b-box p-4">{children}</div>
  </>
}

// ---- pagination: join of buttons ---------------------------------------------------------------------------------
export function Pager({ page, pages, onPage }: { page: number; pages: number; onPage: (p: number) => void }) {
  if (pages <= 1) return null
  return (
    <nav aria-label="Pagination" className="join my-2">
      <button className="join-item btn btn-sm" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label="Previous page">{'«'}</button>
      <span className="join-item btn btn-sm pointer-events-none" aria-live="polite">Page {page} of {pages}</span>
      <button className="join-item btn btn-sm" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label="Next page">{'»'}</button>
    </nav>
  )
}

// ---- text output as code (UI-003: lines are React text; nothing is interpreted) -------------------------------------
export function MockCode({ text, label, prefix }: { text: string; label: string; prefix?: string }) {
  const lines = text.split('\n')
  return (
    <div className="mockup-code w-full max-h-[28rem] overflow-auto" tabIndex={0} role="region" aria-label={label}>
      {lines.map((l, i) => <pre key={i} data-prefix={prefix ?? String(i + 1)}><code>{l}</code></pre>)}
    </div>
  )
}

// ---- time: countdown (h m s) and radial progress ---------------------------------------------------------------------
const cv = (n: number) => ({ '--value': n } as CSSProperties)
export function Countdown({ ms }: { ms: number }) {
  const s = Math.max(0, Math.floor(ms / 1000))
  const [h, m, sec] = [Math.floor(s / 3600), Math.floor((s % 3600) / 60), s % 60]
  return (
    <span className="countdown font-mono" role="timer" aria-label={`${h} hours ${m} minutes ${sec} seconds left`}>
      <span style={cv(Math.min(h, 99))} aria-hidden="true">{h}</span>:<span style={cv(m)} aria-hidden="true">{m}</span>:<span style={cv(sec)} aria-hidden="true">{sec}</span>
    </span>
  )
}
export const Radial = ({ value, label, children }: { value: number; label: string; children?: ReactNode }) => {
  const v = Math.max(0, Math.min(100, Math.round(value)))
  return <div className="radial-progress text-primary text-xs" style={{ '--value': v, '--size': '3.5rem', '--thickness': '5px' } as CSSProperties} role="progressbar" aria-label={label} aria-valuenow={v} aria-valuemin={0} aria-valuemax={100}>
    <span className="text-base-content">{children ?? `${v}%`}</span></div>
}

// ---- row actions: popover dropdown (top layer, so the table's scroll container cannot clip it) -------------------------
export function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  const id = useId().replace(/:/g, '')
  const anchor = `--a${id}`
  return <>
    <button type="button" className="btn btn-ghost btn-xs" aria-label={label} style={{ anchorName: anchor } as CSSProperties} {...{ popovertarget: id }}><span aria-hidden="true">{'⋯'}</span></button>
    <ul className="dropdown dropdown-end menu bg-base-200 rounded-box shadow-lg w-48" id={id} style={{ positionAnchor: anchor } as CSSProperties} {...{ popover: '' }}>{children}</ul>
  </>
}

// ---- small inline icons (aria-hidden; the control carries the accessible name) ----------------------------------------
export const Icon = ({ d, className = 'size-5' }: { d: string; className?: string }) => (
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true" focusable="false"><path d={d} /></svg>
)
export const ICONS = {
  menu: 'M4 6h16M4 12h16M4 18h16', home: 'M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10', check: 'M5 12l5 5 9-10', key: 'M15 7a4 4 0 11-3 6.6L4 21v-3h3v-3h3l2-2',
  list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01', cog: 'M12 8a4 4 0 100 8 4 4 0 000-8zM12 2v3M12 19v3M2 12h3M19 12h3',
  sun: 'M12 4V2M12 22v-2M4 12H2M22 12h-2M12 16a4 4 0 100-8 4 4 0 000 8z', moon: 'M21 13A9 9 0 1111 3a7 7 0 0010 10z', user: 'M12 12a4 4 0 100-8 4 4 0 000 8zM4 21a8 8 0 0116 0',
  shield: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z',
}
