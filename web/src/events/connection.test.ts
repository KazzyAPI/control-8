import { describe, expect, it } from 'vitest'
import { banner, indicator, initialConn, step } from './connection'

describe('UI-022/023 connection state machine', () => {
  it('goes live, drops, recovers', () => {
    let c = initialConn(0)
    c = step(c, 'open', 1000)
    expect(indicator(c, 1000)).toBe('Live')
    c = step(c, 'error', 2000)
    expect(c.status).toBe('reconnecting')
    expect(banner(c, 6000)).toBe('none') // <= 5 s
    expect(banner(c, 7001)).toBe('amber')
    expect(indicator(c, 7001)).toBe('Reconnecting…')
    expect(banner(c, 32001)).toBe('red')
    expect(indicator(c, 32001)).toBe('Offline')
    c = step(c, 'error', 33000) // repeated errors keep the outage start
    expect(banner(c, 33000)).toBe('red')
    c = step(c, 'open', 34000)
    expect(banner(c, 34000)).toBe('none')
    expect(c.lastLive).toBe(34000)
  })
  it('never going live shows the banner after 5 s', () => {
    expect(banner(initialConn(0), 5001)).toBe('amber')
  })
  it('session_ended is terminal', () => {
    const c = step(initialConn(0), 'session_ended', 10)
    expect(step(c, 'open', 20).status).toBe('ended')
    expect(indicator(c, 10)).toBe('Offline')
  })
})
