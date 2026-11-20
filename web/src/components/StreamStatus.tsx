import { useEffect, useState } from 'react'
import { banner, indicator, type Conn } from '../events/connection'

const useNow = () => {
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])
  return now
}
const hhmmss = (t: number | null) => (t ? new Date(t).toLocaleTimeString([], { hour12: false }) : 'never')

// UI-022
export function ConnectionIndicator({ conn }: { conn: Conn }) {
  const label = indicator(conn, useNow())
  return <span className={`conn ${label === 'Live' ? 'live' : label === 'Offline' ? 'offline' : 'reconnecting'}`}>
    <span aria-hidden="true">{'●'} </span>{label}
  </span>
}

// UI-023
export function StaleBanner({ conn, onRetry }: { conn: Conn; onRetry: () => void }) {
  const level = banner(conn, useNow())
  if (level === 'none') return null
  return (
    <div className={`stale ${level}`} role="status">
      Live updates paused {'—'} showing data from {hhmmss(conn.lastLive ?? conn.since)}. Reconnecting{'…'}
      {level === 'red' && <button onClick={onRetry}>Retry now</button>}
    </div>
  )
}

// UI-025
export function SessionEndedModal({ conn }: { conn: Conn }) {
  if (conn.status !== 'ended') return null
  return (
    <div className="modal-backdrop">
      <div role="alertdialog" aria-modal="true" aria-labelledby="se-title" className="modal">
        <h2 id="se-title">Your session ended</h2>
        <a className="button" href="/login" autoFocus>Log in</a>
      </div>
    </div>
  )
}
