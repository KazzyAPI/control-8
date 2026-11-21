import { useEffect, useState } from 'react'
import { banner, indicator, type Conn } from '../events/connection'

const useNow = () => {
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])
  return now
}
const hhmmss = (t: number | null) => (t ? new Date(t).toLocaleTimeString([], { hour12: false }) : 'never')

// UI-022: status dot + text (never colour alone) with a tooltip explaining the state.
const TIPS = { Live: 'Connected: changes appear as they happen.', 'Reconnecting…': 'Connection lost: retrying, data may be stale.', Offline: 'Offline for over 30 s: data is stale until the stream returns.' }
export function ConnectionIndicator({ conn }: { conn: Conn }) {
  const label = indicator(conn, useNow())
  const tone = label === 'Live' ? 'badge-success' : label === 'Offline' ? 'badge-error' : 'badge-warning'
  return <div className="tooltip tooltip-bottom tooltip-left sm:tooltip-bottom" data-tip={TIPS[label]}>
    <span className={`conn badge ${tone}`} tabIndex={0}>
      <span className={`status bg-current ${label === 'Reconnecting…' ? 'animate-pulse' : ''}`} aria-hidden="true" />
      <span className="max-sm:sr-only">{label}</span>
    </span></div>
}

// UI-023
export function StaleBanner({ conn, onRetry }: { conn: Conn; onRetry: () => void }) {
  const level = banner(conn, useNow())
  if (level === 'none') return null
  return (
    <div className={`alert rounded-none ${level === 'red' ? 'alert-error' : 'alert-warning'}`} role="status">
      <span>Live updates paused {'—'} showing data from {hhmmss(conn.lastLive ?? conn.since)}. Reconnecting{'…'} <span className="loading loading-dots loading-sm align-middle" aria-hidden="true" /></span>
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
        <div className="modal-action"><a className="btn btn-primary" href="/login" autoFocus>Log in</a></div>
      </div>
    </div>
  )
}
