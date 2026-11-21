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
  return <span className={`conn badge ${label === 'Live' ? 'badge-success' : label === 'Offline' ? 'badge-error' : 'badge-warning'}`}>
    <span aria-hidden="true">{'●'} </span>{label}
  </span>
}

// UI-023
export function StaleBanner({ conn, onRetry }: { conn: Conn; onRetry: () => void }) {
  const level = banner(conn, useNow())
  if (level === 'none') return null
  return (
    <div className={`alert rounded-none ${level === 'red' ? 'alert-error' : 'alert-warning'}`} role="status">
      <span>Live updates paused {'—'} showing data from {hhmmss(conn.lastLive ?? conn.since)}. Reconnecting{'…'}</span>
      {level === 'red' && <button className="btn btn-sm" onClick={onRetry}>Retry now</button>}
    </div>
  )
}

// UI-025
export function SessionEndedModal({ conn }: { conn: Conn }) {
  if (conn.status !== 'ended') return null
  return (
    <div className="modal modal-open">
      <div role="alertdialog" aria-modal="true" aria-labelledby="se-title" className="modal-box">
        <h2 id="se-title" className="text-lg font-bold">Your session ended</h2>
        <a className="btn btn-primary mt-4" href="/login" autoFocus>Log in</a>
      </div>
    </div>
  )
}
