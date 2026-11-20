import { useQuery } from '@tanstack/react-query'
import { Link, Route, Routes, useParams } from 'react-router'
import { api, setCsrf } from './api/client'
import { SessionEndedModal, ConnectionIndicator, StaleBanner } from './components/StreamStatus'
import { useEventStream } from './events/useEventStream'

async function fetchMe() {
  const { data, response } = await api.GET('/me')
  if (response.status === 401) { window.location.assign('/login'); return new Promise<never>(() => {}) } // UI-002: login is a full-page nav
  if (!data) throw new Error(`GET /me ${response.status}`)
  setCsrf(data.csrf_token ?? null)
  return data
}

function Placeholder({ what }: { what: string }) {
  const p = useParams()
  return <p>{what}{Object.keys(p).length ? ` (${Object.values(p).join(' / ')})` : ''} — coming in M14.</p>
}

function Shell({ me }: { me: NonNullable<ReturnType<typeof useMe>['data']> }) {
  const { conn, retry } = useEventStream()
  return (
    <>
      <a className="skip" href="#main">Skip to content</a>
      <header>
        <nav aria-label="Main"><Link to="/">Control 8</Link></nav>
        <ConnectionIndicator conn={conn} />
        <span className="who">{me.display_name}{me.auth_method === 'break_glass' && ' (break-glass)'}</span>
      </header>
      {me.auth_method === 'break_glass' && <div className="banner-red" role="status">Break-glass session</div>}
      <StaleBanner conn={conn} onRetry={retry} />
      <main id="main">
        <Routes>
          <Route path="/" element={<><h1>Overview</h1><Placeholder what="Overview" /></>} />
          <Route path="/n/:ns/s/:stack" element={<><h1>Stack</h1><Placeholder what="Stack" /></>} />
          <Route path="*" element={<><h1>Not found</h1><Link to="/">Back to overview</Link></>} />
        </Routes>
      </main>
      <SessionEndedModal conn={conn} />
    </>
  )
}

const useMe = () => useQuery({ queryKey: ['me'], queryFn: fetchMe })

export default function App() {
  const { data: me, error } = useMe()
  if (error) return <p role="alert">Could not load your session: {String(error)}</p>
  if (!me) return <p>Loading{'…'}</p>
  return <Shell me={me} />
}
