package events

import (
	"testing"
	"time"
)

// EVT-005: at most one call per key per second, trailing flush runs once (fake clock).
func TestEVT005_Coalescer(t *testing.T) {
	now := time.Unix(1000, 0)
	var pending []func()
	c := NewCoalescer(time.Second)
	c.Now = func() time.Time { return now }
	c.After = func(_ time.Duration, f func()) { pending = append(pending, f) }
	n := 0
	f := func() { n++ }
	c.Do("d1", f) // immediate
	for i := 0; i < 5; i++ {
		now = now.Add(100 * time.Millisecond)
		c.Do("d1", f) // absorbed into one trailing flush
	}
	c.Do("d2", f) // other key is independent
	if n != 2 || len(pending) != 1 {
		t.Fatalf("n=%d pending=%d", n, len(pending))
	}
	now = now.Add(time.Second)
	pending[0]()
	if n != 3 {
		t.Fatalf("trailing flush: n=%d", n)
	}
	c.Do("d1", f) // flush counted as the last send: inside the window again
	if n != 3 || len(pending) != 2 {
		t.Fatalf("n=%d pending=%d", n, len(pending))
	}
}
