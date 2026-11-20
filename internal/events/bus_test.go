package events

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Unix(1732183200, 0)

func TestEVT002_MonotonicOrder(t *testing.T) { // EVT-002
	b := New(10, t0)
	s, _, _ := b.Subscribe(nil)
	for i := 0; i < 3; i++ {
		b.Publish(Event{Type: "x"})
	}
	prev := uint64(0)
	for i := 0; i < 3; i++ {
		e := <-s.C
		if e.ID <= prev {
			t.Fatalf("ids not increasing: %d after %d", e.ID, prev)
		}
		prev = e.ID
	}
	if b2 := New(10, t0.Add(time.Second)); b2.next <= prev {
		t.Fatal("restart IDs went backwards")
	}
}

func TestEVT003_ReplayAndReset(t *testing.T) { // EVT-003, EVT-011
	b := New(3, t0)
	for i := 0; i < 5; i++ { // ring keeps the last 3
		b.Publish(Event{Type: "x"})
	}
	first := b.base
	cases := []struct {
		last      uint64
		reset     bool
		replayLen int
	}{
		{first - 1, false, 3}, {first, false, 2}, {b.next - 1, false, 0},
		{first - 2, true, 0}, {b.next, true, 0}, {0, true, 0},
	}
	for _, c := range cases {
		last := c.last
		s, rep, reset := b.Subscribe(&last)
		b.Unsubscribe(s)
		if reset != c.reset || len(rep) != c.replayLen {
			t.Errorf("last=%d: reset=%v replay=%d, want %v/%d", c.last, reset, len(rep), c.reset, c.replayLen)
		}
	}
}

func TestEVT001_SlowConsumerDroppedNonBlocking(t *testing.T) { // EVT-001
	b := New(10, t0)
	slow, _, _ := b.Subscribe(nil)
	fast, _, _ := b.Subscribe(nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			b.Publish(Event{Type: "x"})
			select {
			case <-fast.C:
			default:
			}
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher blocked")
	}
	n := 0
	for range slow.C { // closed by the bus after 256 buffered
		n++
	}
	if n != 256 {
		t.Fatalf("slow got %d, want 256", n)
	}
}

func TestEVT001_Race(t *testing.T) {
	b := New(50, t0)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				b.Publish(Event{Type: "x"})
			}
		}()
		go func() {
			defer wg.Done()
			s, _, _ := b.Subscribe(nil)
			for j := 0; j < 20; j++ {
				select {
				case <-s.C:
				default:
				}
			}
			b.Unsubscribe(s)
		}()
	}
	wg.Wait()
}
