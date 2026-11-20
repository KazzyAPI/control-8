import { useEffect, useRef } from 'react'
import { Link, Route, Routes } from 'react-router'
import { useMe } from './lib'
import { ToastProvider, useToast } from './ui'
import { Asset, Namespace, Overview } from './screens/Browse'
import { Deployment, Revision, Stack } from './screens/Stack'
import { AdminAccess, AdminPolicy, Approvals, ApproverQueue } from './screens/Access'
import { NewRevision, RequestAccess } from './screens/Write'
import { Audit, Grants, Targets } from './screens/Feeds'
import { SessionEndedModal, ConnectionIndicator, StaleBanner } from './components/StreamStatus'
import { useEventStream } from './events/useEventStream'

function Shell({ me }: { me: NonNullable<ReturnType<typeof useMe>['data']> }) {
  const { conn, retry } = useEventStream()
  const { say } = useToast()
  const n = me.active_grants?.length ?? 0
  const prev = useRef(n)
  useEffect(() => { if (n !== prev.current) say(n > prev.current ? 'Prod access granted' : 'Prod access ended'); prev.current = n }, [n, say]) // UI-031
  return (
    <>
      <a className="skip" href="#main">Skip to content</a>
      <header>
        <nav aria-label="Main">
          <Link to="/">Control 8</Link> <Link to="/approvals">Approvals</Link> <Link to="/access">My grants</Link> <Link to="/access/request">Request access</Link>
          {(me.global_admin || !!me.approver_for?.length) && <> <Link to="/access/approvals">Approver queue</Link></>} <Link to="/access/active">Active grants</Link> <Link to="/audit">Audit</Link>
          {me.global_admin && <> <Link to="/admin/access">Admin: access</Link> <Link to="/admin/policy">Admin: policy</Link> <Link to="/admin/targets">Targets</Link></>}
        </nav>
        <ConnectionIndicator conn={conn} />
        <span className="who">{me.display_name}{me.auth_method === 'break_glass' && ' (break-glass)'}</span>
      </header>
      {me.auth_method === 'break_glass' && <div className="banner-red" role="status">Break-glass session</div>}
      <StaleBanner conn={conn} onRetry={retry} />
      <main id="main">
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
          <Route path="*" element={<><h1>Not found</h1><Link to="/">Back to overview</Link></>} />
        </Routes>
      </main>
      <SessionEndedModal conn={conn} />
    </>
  )
}

export default function App() {
  const { data: me, error } = useMe()
  if (error) return <p role="alert">Could not load your session: {String(error)}</p>
  if (!me) return <p>Loading{'…'}</p>
  return <ToastProvider><Shell me={me} /></ToastProvider>
}
