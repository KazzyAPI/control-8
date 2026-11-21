import { useEffect, useRef } from 'react'
import { Link, NavLink, Route, Routes } from 'react-router'
import { useMe } from './lib'
import { ToastProvider, useToast } from './ui'
import { Asset, Namespace, Overview } from './screens/Browse'
import { Deployment, Revision, Stack } from './screens/Stack'
import { AdminAccess, AdminPolicy, Approvals, ApproverQueue } from './screens/Access'
import { NewRevision, RequestAccess } from './screens/Write'
import { Audit, Grants, Targets } from './screens/Feeds'
import { SessionEndedModal, ConnectionIndicator, StaleBanner } from './components/StreamStatus'
import Login from './screens/Login'
import { useEventStream } from './events/useEventStream'

function Shell({ me }: { me: NonNullable<ReturnType<typeof useMe>['data']> }) {
  const { conn, retry } = useEventStream()
  const { say } = useToast()
  const n = me.active_grants?.length ?? 0
  const prev = useRef(n)
  useEffect(() => { if (n !== prev.current) say(n > prev.current ? 'Prod access granted' : 'Prod access ended'); prev.current = n }, [n, say]) // UI-031
  const links: [string, string][] = [['/approvals', 'Approvals'], ['/access', 'My grants'], ['/access/request', 'Request access'],
    ...(me.global_admin || me.approver_for?.length ? [['/access/approvals', 'Approver queue'] as [string, string]] : []), ['/access/active', 'Active grants'], ['/audit', 'Audit'],
    ...(me.global_admin ? [['/admin/access', 'Admin: access'], ['/admin/policy', 'Admin: policy'], ['/admin/targets', 'Targets']] as [string, string][] : [])]
  return (
    <>
      <a className="skip btn btn-sm" href="#main">Skip to content</a>
      <header className="navbar bg-base-200 gap-2 flex-wrap">
        <nav aria-label="Main" className="flex-1">
          <ul className="menu menu-horizontal menu-sm flex-wrap">
            <li><Link to="/" className="font-bold text-base">Control 8</Link></li>
            {links.map(([to, text]) => <li key={to}><NavLink to={to} end className={({ isActive }) => (isActive ? 'menu-active' : '')}>{text}</NavLink></li>)}
          </ul>
        </nav>
        <ConnectionIndicator conn={conn} />
        <span className="who font-medium">{me.display_name}{me.auth_method === 'break_glass' && ' (break-glass)'}</span>
      </header>
      {me.auth_method === 'break_glass' && <div className="alert alert-error rounded-none" role="status">Break-glass session</div>}
      <StaleBanner conn={conn} onRetry={retry} />
      <main id="main" className="p-4 max-w-7xl mx-auto">
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
          <Route path="*" element={<><h1>Not found</h1><Link className="link" to="/">Back to overview</Link></>} />
        </Routes>
      </main>
      <SessionEndedModal conn={conn} />
    </>
  )
}

function Authed() {
  const { data: me, error } = useMe()
  if (error) return <p className="alert alert-error m-4" role="alert">Could not load your session: {String(error)}</p>
  if (!me) return <p className="p-4"><span className="loading loading-spinner" aria-hidden="true" /> Loading{'…'}</p>
  return <ToastProvider><Shell me={me} /></ToastProvider>
}

export default function App() {
  return <Routes><Route path="/login" element={<Login />} /><Route path="*" element={<Authed />} /></Routes>
}
