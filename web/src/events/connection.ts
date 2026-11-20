// Connection state machine (UI-022/023). Pure: time is passed in.
export type Status = 'connecting' | 'live' | 'reconnecting' | 'ended'
export type Conn = { status: Status; since: number; lastLive: number | null } // since = when status last changed
export type Action = 'open' | 'error' | 'session_ended'

export const initialConn = (now: number): Conn => ({ status: 'connecting', since: now, lastLive: null })

export function step(c: Conn, a: Action, now: number): Conn {
  if (c.status === 'ended') return c
  if (a === 'session_ended') return { ...c, status: 'ended', since: now }
  if (a === 'open') return c.status === 'live' ? c : { status: 'live', since: now, lastLive: now }
  // error: keep the original outage start
  return c.status === 'live' ? { status: 'reconnecting', since: now, lastLive: now } : { ...c, status: 'reconnecting' }
}

export type Banner = 'none' | 'amber' | 'red'
export const STALE_AMBER_MS = 5000
export const STALE_RED_MS = 30000

export function banner(c: Conn, now: number): Banner {
  if (c.status === 'live' || c.status === 'ended') return 'none'
  const down = now - c.since
  return down > STALE_RED_MS ? 'red' : down > STALE_AMBER_MS ? 'amber' : 'none'
}

/** Header indicator: Live, Reconnecting…, or Offline (down > 30 s). */
export const indicator = (c: Conn, now: number): 'Live' | 'Reconnecting…' | 'Offline' =>
  c.status === 'live' ? 'Live' : banner(c, now) === 'red' || c.status === 'ended' ? 'Offline' : 'Reconnecting…'
