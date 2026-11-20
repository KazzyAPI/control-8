import { Link, Route, Routes } from 'react-router'
import { useMe } from './lib'
import { Asset, Namespace, Overview } from './screens/Browse'
import { Deployment, Revision, Stack } from './screens/Stack'
import { Audit, Grants, Targets } from './screens/Feeds'
import { SessionEndedModal, ConnectionIndicator, StaleBanner } from './components/StreamStatus'
import { useEventStream } from './events/useEventStream'

function Shell({ me }: { me: NonNullable<ReturnType<typeof useMe>['data']> }) {
  const { conn, retry } = useEventStream()
  return (
    <>
      <a className="skip" href="#main">Skip to content</a>
      <header>
        <nav aria-label="Main">
          <Link to="/">Control 8</Link> <Link to="/access">My grants</Link> <Link to="/access/active">Active grants</Link> <Link to="/audit">Audit</Link>
          {me.global_admin && <> <Link to="/admin/targets">Targets</Link></>}
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
          <Route path="/n/:ns/s/:stack/revisions/:n" element={<Revision />} />
          <Route path="/deployments/:id" element={<Deployment />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/access" element={<Grants />} />
          <Route path="/access/active" element={<Grants active />} />
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
  return <Shell me={me} />
}
