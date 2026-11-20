import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { applyEvent, EVENT_TYPES, type C8Event } from './dispatch'
import { initialConn, step, type Action, type Conn } from './connection'

const RETRY_MS = 2000

// UI-020/021: one EventSource per tab. Native reconnect handles transient drops; if the browser gives up
// (CLOSED, e.g. a proxy 502) we reopen ourselves with ?last_event_id.
export function useEventStream() {
  const qc = useQueryClient()
  const [conn, setConn] = useState<Conn>(() => initialConn(Date.now()))
  const connRef = useRef(conn)
  const lastId = useRef('')
  const retry = useRef<() => void>(() => {})

  const send = useCallback((a: Action) => {
    connRef.current = step(connRef.current, a, Date.now())
    setConn(connRef.current)
  }, [])

  useEffect(() => {
    let es: EventSource | undefined
    let timer: ReturnType<typeof setTimeout> | undefined
    let stopped = false
    let gap = false // a reconnect after a gap refetches everything (UI-020)

    const setLive = (live: boolean) =>
      qc.setDefaultOptions({ queries: { staleTime: live ? Infinity : 0, refetchInterval: live ? false : 30000 } })

    const open = () => {
      if (stopped) return
      es = new EventSource('/api/v1/events' + (lastId.current ? `?last_event_id=${lastId.current}` : ''))
      es.onopen = () => {
        setLive(true)
        if (gap) qc.invalidateQueries()
        gap = false
        send('open')
      }
      es.onerror = () => {
        gap = true
        setLive(false)
        send('error')
        if (es?.readyState === EventSource.CLOSED) timer = setTimeout(open, RETRY_MS)
      }
      for (const t of EVENT_TYPES) {
        es.addEventListener(t, (m) => {
          lastId.current = (m as MessageEvent).lastEventId || lastId.current
          const ev = JSON.parse((m as MessageEvent).data) as C8Event
          if (ev.type === 'session.ended') {
            stopped = true
            es?.close()
            send('session_ended')
          } else applyEvent(qc, ev)
        })
      }
    }
    retry.current = () => { clearTimeout(timer); es?.close(); open() }
    open()
    return () => { stopped = true; clearTimeout(timer); es?.close() }
  }, [qc, send])

  return { conn, retry: () => retry.current() }
}
