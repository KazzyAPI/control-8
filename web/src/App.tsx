import { useEffect, useRef, useState } from 'react'
import { Route, Routes, useLocation } from 'react-router'
import { useMe } from './lib'
import { ToastProvider, useToast } from './ui'
import { Asset, Namespace, Overview } from './screens/Browse'
import { Deployment, Revision, Stack } from './screens/Stack'
import { AdminAccess, AdminPolicy, Approvals, ApproverQueue } from './screens/Access'
import { NewRevision, RequestAccess } from './screens/Write'
import { Audit, Grants, Targets } from './screens/Feeds'
import { SessionEndedModal, ConnectionIndicator, StaleBanner } from './components/StreamStatus'
import { Breadcrumbs, BottomDock, Footer, Sidebar, ThemeSwitcher, UserMenu, useNavCounts } from './components/Nav'
import { Icon, Notice } from './components/ui'
import Login from './screens/Login'
import { Link } from 'react-router'

// 404: daisyUI hero with a clear way back.
function NotFound() {
  return (
    <div className="hero min-h-[60vh] rounded-box bg-base-200">
      <div className="hero-content flex-col text-center gap-2">
        <p className="text-8xl font-black text-primary" aria-hidden="true">404</p>
        <h1 className="text-2xl font-bold">Not found</h1>
        <p className="max-w-sm">That page does not exist, or you do not have access to it.</p>
        <Link className="btn btn-primary mt-2" to="/"><Icon name="home" className="size-4" />Back to overview</Link>
      </div>
    </div>
  )
}
import { useEventStream } from './events/useEventStream'

function Shell({ me }: { me: NonNullable<ReturnType<typeof useMe>['data']> }) {
  const { conn, retry } = useEventStream()
  const { say } = useToast()
  const counts = useNavCounts(me)
  const loc = useLocation()
  const [drawer, setDrawer] = useState(false)
  const n = me.active_grants?.length ?? 0
  const prev = useRef(n)
  useEffect(() => { if (n !== prev.current) say(n > prev.current ? 'Prod access granted' : 'Prod access ended'); prev.current = n }, [n, say]) // UI-031
  useEffect(() => setDrawer(false), [loc.pathname])
  useEffect(() => {
    if (!drawer) return
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') setDrawer(false) }
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  }, [drawer])
  return (
    <div className="drawer lg:drawer-open">
      <input id="nav-drawer" type="checkbox" className="drawer-toggle" checked={drawer} onChange={(e) => setDrawer(e.target.checked)} tabIndex={-1} aria-hidden="true" />
      <div className="drawer-content flex flex-col min-h-screen min-w-0">
        <a className="skip btn btn-sm" href="#main">Skip to content</a>
        <header className="navbar bg-base-100 border-b border-base-300 sticky top-0 z-20 gap-2 min-h-14">
          <button type="button" className="btn btn-square btn-ghost lg:hidden" aria-label="Open navigation" aria-expanded={drawer} onClick={() => setDrawer(!drawer)}><Icon name="menu" /></button>
          <div className="flex-1 min-w-0"><Breadcrumbs /></div>
          <ConnectionIndicator conn={conn} />
          <ThemeSwitcher />
          <UserMenu me={me} />
        </header>
        {me.auth_method === 'break_glass' && <Notice tone="error" className="rounded-none py-2"><strong>Break-glass session.</strong> Every action is audited; approvals are disabled.</Notice>}
        <StaleBanner conn={conn} onRetry={retry} />
        <main id="main" className="overflow-x-clip min-w-0 p-4 sm:p-6 max-w-7xl w-full mx-auto flex-1 pb-24 lg:pb-6">
          <Routes>
            <Route path="/" element={<Overview />} />
            <Route path="/n/:ns" element={<Namespace />} />
            <Route path="/n/:ns/assets/:asset" element={<Asset />} />
            <Route path="/n/:ns/s/:stack" element={<Stack />} />
            <Route path="/n/:ns/s/:stack/revisions/new" element={<NewRevision />} />
            <Route path="/n/:ns/s/:stack/revisions/:n" element={<Revision />} />
            <Route path="/deployments/:id" element={<Deployment />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/access" element={<Grants />} />
            <Route path="/access/active" element={<Grants active />} />
            <Route path="/access/request" element={<RequestAccess />} />
            <Route path="/access/approvals" element={<ApproverQueue />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/admin/access" element={<AdminAccess />} />
            <Route path="/admin/policy" element={<AdminPolicy />} />
            <Route path="/admin/targets" element={<Targets />} />
            <Route path="*" element={<NotFound />} />
          </Routes>
        </main>
        <Footer />
        <BottomDock counts={counts} onMenu={() => setDrawer(true)} />
        <SessionEndedModal conn={conn} />
      </div>
      <div className="drawer-side z-40">
        <label htmlFor="nav-drawer" aria-hidden="true" className="drawer-overlay" />
        <Sidebar me={me} counts={counts} />
      </div>
    </div>
  )
}

function Authed() {
  const { data: me, error } = useMe()
  if (error) return <div className="p-4"><Notice tone="error" role="alert">Could not load your session: {String(error)}</Notice></div>
  if (!me) return (
    <div className="flex min-h-screen" role="status"><span className="sr-only">Loading{'…'}</span>
      <div className="hidden lg:block w-64 bg-base-200 p-6 space-y-4" aria-hidden="true"><div className="skeleton h-6 w-32" />{[0, 1, 2, 3, 4].map((i) => <div key={i} className="skeleton h-4 w-40" />)}</div>
      <div className="flex-1" aria-hidden="true"><div className="navbar border-b border-base-300"><div className="skeleton h-4 w-48" /></div><div className="p-4 space-y-4"><div className="skeleton h-8 w-64" /><div className="skeleton h-40 w-full" /></div></div>
    </div>
  )
  return <ToastProvider><Shell me={me} /></ToastProvider>
}

export default function App() {
  return <Routes><Route path="/login" element={<Login />} /><Route path="*" element={<Authed />} /></Routes>
}
