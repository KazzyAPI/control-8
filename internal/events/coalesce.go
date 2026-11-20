package events

import (
	"sync"
	"time"
)

// Coalescer limits a high-frequency source to one call per key per Every, latest wins (EVT-005): a call inside
// the window schedules one trailing flush and further calls until it fires are absorbed. f must read the latest
// state itself when it runs. Now/After are replaceable for tests.
type Coalescer struct {
	Every time.Duration
	Now   func() time.Time
	After func(d time.Duration, f func())
	mu    sync.Mutex
	last  map[string]time.Time
	wait  map[string]bool
}

func NewCoalescer(every time.Duration) *Coalescer {
	return &Coalescer{Every: every, Now: time.Now, After: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		last: map[string]time.Time{}, wait: map[string]bool{}}
}

func (c *Coalescer) Do(key string, f func()) {
	c.mu.Lock()
	if c.wait[key] {
		c.mu.Unlock()
		return
	}
	if d := c.Every - c.Now().Sub(c.last[key]); d > 0 {
		c.wait[key] = true
		c.mu.Unlock()
		c.After(d, func() {
			c.mu.Lock()
			c.wait[key], c.last[key] = false, c.Now()
			c.mu.Unlock()
			f()
		})
		return
	}
	c.last[key] = c.Now()
	c.mu.Unlock()
	f()
}
