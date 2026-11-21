import { useId, useState, type CSSProperties, type ReactNode } from 'react'
import { useFlash } from './useFlash'
import { EmptyState } from './EmptyState'
import { Icon } from './Icon'

// UI-004: tables scroll inside their own focusable container (axe: scrollable-region-focusable).
export type Column<T> = { key: string; header: string; cell: (row: T) => ReactNode; rowHeader?: boolean; hideBelow?: 'sm' | 'md' | 'lg'; className?: string }
const HIDE = { sm: 'max-sm:hidden', md: 'max-md:hidden', lg: 'max-lg:hidden' }

function Row<T>({ row, columns, actions, actionsLabel, sig }: { row: T; columns: Column<T>[]; actions?: (r: T) => ReactNode; actionsLabel?: (r: T) => string; sig?: (r: T) => string }) {
  const n = useFlash(sig ? sig(row) : '') // UI-024: a changed row flashes (keyed so the animation restarts)
  return (
    <tr key={n} className={`hover ${n ? 'flash' : ''}`}>
      {columns.map((c) => {
        const cls = `${c.hideBelow ? HIDE[c.hideBelow] : ''} ${c.className ?? ''}`
        return c.rowHeader ? <th key={c.key} scope="row" className={cls}>{c.cell(row)}</th> : <td key={c.key} className={cls}>{c.cell(row)}</td>
      })}
      {actions && <td className="w-px text-right">{<RowMenu label={actionsLabel ? actionsLabel(row) : 'Row actions'}>{actions(row)}</RowMenu>}</td>}
    </tr>
  )
}

// daisyUI `table` (zebra, sticky header) with loading skeleton rows, an empty state, optional per-row actions dropdown and `join` pagination.
export function DataTable<T>({ label, columns, rows, rowKey, rowActions, actionsLabel, rowSig, loading, empty, pageSize, maxHeight = '70vh' }: {
  label: string; columns: Column<T>[]; rows: T[]; rowKey: (r: T) => string; rowActions?: (r: T) => ReactNode; actionsLabel?: (r: T) => string
  rowSig?: (r: T) => string; loading?: boolean; empty?: ReactNode; pageSize?: number; maxHeight?: string
}) {
  const [page, setPage] = useState(1)
  if (loading) return <TableSkeleton cols={columns.length + (rowActions ? 1 : 0)} />
  if (!rows.length) return <>{empty ?? <EmptyState>Nothing to show.</EmptyState>}</>
  const pages = pageSize ? Math.max(1, Math.ceil(rows.length / pageSize)) : 1
  const p = Math.min(page, pages)
  const view = pageSize ? rows.slice((p - 1) * pageSize, p * pageSize) : rows
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-auto rounded-box border border-base-300 bg-base-100" style={{ maxHeight }} tabIndex={0} role="region" aria-label={label}>
        <table className="table table-zebra table-pin-rows">
          <thead><tr>{columns.map((c) => <th key={c.key} scope="col" className={c.hideBelow ? HIDE[c.hideBelow] : ''}>{c.header}</th>)}{rowActions && <th scope="col" className="w-px"><span className="sr-only">Actions</span></th>}</tr></thead>
          <tbody>{view.map((r) => <Row key={rowKey(r)} row={r} columns={columns} actions={rowActions} actionsLabel={actionsLabel} sig={rowSig} />)}</tbody>
        </table>
      </div>
      <Pager page={p} pages={pages} onPage={setPage} />
    </div>
  )
}

export function TableSkeleton({ rows = 4, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <div role="status">
      <span className="sr-only">Loading{'…'}</span>
      <div aria-hidden="true" className="overflow-x-auto rounded-box border border-base-300 bg-base-100">
        <table className="table"><thead><tr>{Array.from({ length: cols }, (_, i) => <th key={i}><div className="skeleton h-4 w-20" /></th>)}</tr></thead>
          <tbody>{Array.from({ length: rows }, (_, r) => <tr key={r}>{Array.from({ length: cols }, (_, c) => <td key={c}><div className={`skeleton h-4 ${c === 0 ? 'w-32' : 'w-16'}`} /></td>)}</tr>)}</tbody></table>
      </div>
    </div>
  )
}

// Pagination as a daisyUI `join` of buttons.
export function Pager({ page, pages, onPage }: { page: number; pages: number; onPage: (p: number) => void }) {
  if (pages <= 1) return null
  return (
    <nav aria-label="Pagination" className="join self-center">
      <button className="join-item btn btn-sm" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label="Previous page">{'«'}</button>
      <span className="join-item btn btn-sm pointer-events-none" aria-live="polite">Page {page} of {pages}</span>
      <button className="join-item btn btn-sm" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label="Next page">{'»'}</button>
    </nav>
  )
}

// Row actions: daisyUI `dropdown` driven by the popover API (top layer, so the table's scroll container cannot clip it).
export function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  const id = useId().replace(/:/g, '')
  const anchor = `--a${id}`
  return <>
    <button type="button" className="btn btn-ghost btn-xs btn-square tooltip tooltip-left" data-tip={label} aria-label={label} style={{ anchorName: anchor } as CSSProperties} {...{ popovertarget: id }}><Icon name="ellipsis" className="size-5" /></button>
    <ul className="dropdown dropdown-end menu bg-base-100 rounded-box border border-base-300 shadow-lg w-52" id={id} style={{ positionAnchor: anchor } as CSSProperties} {...{ popover: '' }}>{children}</ul>
  </>
}
