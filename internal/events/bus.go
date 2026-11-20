// Package events is the in-process event bus with a replay ring (10-events EVT-001..004, D-55).
package events

import (
	"sync"
	"time"
)

type Event struct {
	ID        uint64    `json:"-"`
	Type      string    `json:"type"`
	At        time.Time `json:"at"`
	Namespace string    `json:"namespace"`
	Data      any       `json:"data"`
	Requester string    `json:"-"` // access_grant.updated audience (user id)
}

// Sub is one subscriber. C is closed when the bus drops it (slow consumer, EVT-001) or on Unsubscribe.
type Sub struct{ C chan Event }

type Bus struct {
	mu    sync.Mutex
	ring  []Event
	cap   int
	next  uint64 // ID of the next event
	base  uint64 // ID of the oldest retained event (== next when empty)
	subs  map[*Sub]struct{}
	chbuf int
}

// New starts IDs at now-ms*1000 so they never go backwards across restarts (EVT-002).
func New(replay int, now time.Time) *Bus {
	if replay <= 0 {
		replay = 10000
	}
	id := uint64(now.UnixMilli()) * 1000
	return &Bus{cap: replay, next: id, base: id, subs: map[*Sub]struct{}{}, chbuf: 256}
}

// Publish never blocks and never fails the caller (EVT-001, EVT-004).
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e.ID = b.next
	b.next++
	b.ring = append(b.ring, e)
	if len(b.ring) > b.cap {
		b.ring = b.ring[1:]
		b.base++
	}
	for s := range b.subs {
		select {
		case s.C <- e:
		default:
			delete(b.subs, s)
			close(s.C)
		}
	}
}

// Subscribe registers a subscriber atomically with the replay snapshot, so no event is missed or duplicated.
// last is the Last-Event-ID (nil = none). reset is true when it cannot be resumed (EVT-011).
func (b *Bus) Subscribe(last *uint64) (s *Sub, replay []Event, reset bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if last != nil {
		if *last+1 < b.base || *last >= b.next {
			reset = true
		} else {
			replay = append(replay, b.ring[*last+1-b.base:]...)
		}
	}
	s = &Sub{C: make(chan Event, b.chbuf)}
	b.subs[s] = struct{}{}
	return
}

func (b *Bus) Unsubscribe(s *Sub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.C)
	}
}
