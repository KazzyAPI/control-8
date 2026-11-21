import { TableSkeleton } from './DataTable'

// Loading states shaped like the content they stand in for; one polite "Loading" status each (the shapes are aria-hidden).
const Loading = () => <span className="sr-only">Loading{'…'}</span>
export const SkStats = ({ n = 4 }: { n?: number }) => (
  <div role="status"><Loading />
    <div aria-hidden="true" className="grid gap-4 grid-cols-1 sm:grid-cols-2 xl:grid-cols-4">
      {Array.from({ length: n }, (_, i) => <div key={i} className="stats border border-base-300 bg-base-100"><div className="stat"><div className="skeleton h-3 w-20 mb-2" /><div className="skeleton h-8 w-16 mb-2" /><div className="skeleton h-3 w-28" /></div></div>)}
    </div></div>
)
export const SkCards = ({ n = 3 }: { n?: number }) => (
  <div role="status"><Loading />
    <div aria-hidden="true" className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {Array.from({ length: n }, (_, i) => <div key={i} className="card card-border bg-base-100"><div className="card-body gap-3">
        <div className="skeleton h-5 w-32" /><div className="skeleton h-4 w-full" /><div className="skeleton h-4 w-2/3" /></div></div>)}
    </div></div>
)
export const SkText = ({ lines = 3 }: { lines?: number }) => (
  <div role="status" className="flex flex-col gap-2"><Loading />
    {Array.from({ length: lines }, (_, i) => <div key={i} aria-hidden="true" className={`skeleton h-4 ${i === lines - 1 ? 'w-2/3' : 'w-full'}`} />)}</div>
)
export const SkPage = () => (
  <div role="status"><Loading />
    <div aria-hidden="true" className="flex flex-col gap-5">
      <div className="flex items-center gap-4"><div className="skeleton size-12 rounded-box" /><div className="flex flex-col gap-2"><div className="skeleton h-7 w-64" /><div className="skeleton h-4 w-40" /></div></div>
      <div className="grid gap-4 grid-cols-1 sm:grid-cols-3">{[0, 1, 2].map((i) => <div key={i} className="skeleton h-24" />)}</div>
      <div className="skeleton h-48 w-full" />
    </div></div>
)
export type Fallback = 'table' | 'cards' | 'stats' | 'text' | 'page'
export const Skeleton = ({ kind }: { kind: Fallback }) =>
  kind === 'table' ? <TableSkeleton /> : kind === 'cards' ? <SkCards /> : kind === 'stats' ? <SkStats /> : kind === 'page' ? <SkPage /> : <SkText />
