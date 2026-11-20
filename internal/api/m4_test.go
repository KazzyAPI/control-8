package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/events"
)

type msg struct{ id, event, data string }

type sseConn struct {
	ch     chan msg
	status int
	cancel context.CancelFunc
}

// open starts a stream against a real httptest server and parses SSE frames in a goroutine.
func open(t *testing.T, ts *httptest.Server, hdr map[string]string, query string) *sseConn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/events"+query, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	c := &sseConn{ch: make(chan msg, 100), status: resp.StatusCode, cancel: cancel}
	go func() {
		defer close(c.ch)
		defer resp.Body.Close()
		var m msg
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			l := sc.Text()
			switch {
			case l == ": ping":
				c.ch <- msg{event: "ping"}
			case strings.HasPrefix(l, "id: "):
				m.id = l[4:]
			case strings.HasPrefix(l, "event: "):
				m.event = l[7:]
			case strings.HasPrefix(l, "data: "):
				m.data = l[6:]
			case l == "" && m.event != "":
				c.ch <- m
				m = msg{}
			}
		}
	}()
	return c
}

func (c *sseConn) next(t *testing.T) (m msg, ok bool) {
	t.Helper()
	select {
	case m, ok = <-c.ch:
		return
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for SSE message")
	}
	return
}

func (c *sseConn) expect(t *testing.T, event, contains string) msg {
	t.Helper()
	m, ok := c.next(t)
	if !ok || m.event != event || !strings.Contains(m.data, contains) {
		t.Fatalf("got %+v ok=%v, want %s containing %q", m, ok, event, contains)
	}
	return m
}

func setup(t *testing.T) (*env, *httptest.Server, map[string]string) {
	e := newEnv(t)
	ts := httptest.NewServer(e.h)
	t.Cleanup(ts.Close)
	admin, _ := e.login()
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','va','a','viewer')`)
	return e, ts, admin
}

func mkNS(e *env, admin map[string]string, slug string) {
	e.do("POST", "/api/v1/namespaces", `{"slug":"`+slug+`","display_name":"X","environment_tier":"non-prod"}`, admin)
}

func TestEVT_AuditCreatedPerViewerFilter(t *testing.T) { // EVT-010, EVT-012, AUTH-040, AUTH-041, AUTH-043, R-8
	e, ts, admin := setup(t)
	va := e.user("va", `["va"]`)
	vc := open(t, ts, va, "")
	ac := open(t, ts, admin, "")
	if vc.status != 200 || ac.status != 200 {
		t.Fatalf("status %d %d", vc.status, ac.status)
	}
	if r, _ := http.Get(ts.URL + "/api/v1/events"); r.StatusCode != 401 {
		t.Fatalf("anonymous = %d", r.StatusCode)
	}
	mkNS(e, admin, "b") // audit for b: viewer of a must not see it
	// global audit (csrf denial has no namespace): global_admin only
	bad := map[string]string{"Cookie": admin["Cookie"], "X-CSRF-Token": "bad"}
	e.do("POST", "/api/v1/namespaces", `{}`, bad)
	mkNS(e, admin, "a") // audit for a: viewer receives it
	vm := vc.expect(t, "audit.created", `"namespace":"a"`)
	if !strings.Contains(vm.data, `"action":"namespace.create"`) || vm.id == "" {
		t.Fatalf("viewer event %+v", vm)
	}
	for _, want := range []string{`"namespace":"b"`, `"action":"csrf.check"`, `"namespace":"a"`} {
		ac.expect(t, "audit.created", want)
	}
	// namespace/types query filters
	fc := open(t, ts, admin, "?namespace=a&types=audit.created")
	mkNS(e, admin, "c")
	mkNS(e, admin, "a") // duplicate: still an audited denial/failure in a
	fc.expect(t, "audit.created", `"namespace":"a"`)
}

func TestEVT011_ResumeAndReset(t *testing.T) { // EVT-011, EVT-002
	e, ts, admin := setup(t)
	first := open(t, ts, admin, "")
	mkNS(e, admin, "a")
	m := first.expect(t, "audit.created", `"namespace":"a"`)
	first.cancel()
	mkNS(e, admin, "b") // missed while disconnected
	mkNS(e, admin, "c")
	r := open(t, ts, map[string]string{"Cookie": admin["Cookie"], "Last-Event-ID": m.id}, "")
	r.expect(t, "audit.created", `"namespace":"b"`)
	r.expect(t, "audit.created", `"namespace":"c"`)
	// too old / future / garbage IDs -> one reset, then live events
	for _, q := range []string{"?last_event_id=5", "?last_event_id=99999999999999999999", "?last_event_id=junk"} {
		g := open(t, ts, admin, q)
		g.expect(t, "reset", "resume_unavailable")
		mkNS(e, admin, "z")
		g.expect(t, "audit.created", "")
	}
}

func TestEVT012_014_HeartbeatAndSessionEnded(t *testing.T) { // EVT-012, EVT-014, AUTH-042
	e, ts, admin := setup(t)
	beat := make(chan time.Time)
	e.s.tick = func(time.Duration) (<-chan time.Time, func()) { return beat, func() {} }
	va := e.user("va", `["va"]`)
	c := open(t, ts, va, "")
	time.Sleep(50 * time.Millisecond) // let the handler reach its select
	beat <- time.Now()
	c.expect(t, "ping", "")
	e.s.db.Exec(`DELETE FROM session`) // logout/expiry
	beat <- time.Now()
	c.expect(t, "session.ended", "unauthenticated")
	if _, ok := c.next(t); ok {
		t.Fatal("stream still open after session.ended")
	}
	_ = admin
}

func TestEVT013_StreamLimit(t *testing.T) { // EVT-013
	e, ts, admin := setup(t)
	for i := 0; i < 5; i++ {
		if c := open(t, ts, admin, ""); c.status != 200 {
			t.Fatalf("stream %d = %d", i+1, c.status)
		}
	}
	if c := open(t, ts, admin, ""); c.status != 429 {
		t.Fatalf("6th stream = %d", c.status)
	}
	other := e.user("va", `["va"]`) // another principal is unaffected
	if c := open(t, ts, other, ""); c.status != 200 {
		t.Fatalf("other principal = %d", c.status)
	}
	e.s.cfg.EventsMaxConns = 6 // total cap: 6 open already
	if c := open(t, ts, e.user("x", `[]`), ""); c.status != 429 {
		t.Fatalf("over total cap = %d", c.status)
	}
}

func TestEVT001_SlowConsumerDisconnected(t *testing.T) { // EVT-001 through the handler
	e, ts, admin := setup(t)
	c := open(t, ts, admin, "")
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			e.s.publish(events.Event{Type: "audit.created", At: time.Now(), Namespace: "a", Data: map[string]any{}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publishers blocked")
	}
	n := 0
	for range c.ch { // the stream ends once the bus drops the subscriber
		if n++; n > 1000 {
			break
		}
	}
	if n == 0 || n > 300 {
		t.Fatalf("slow consumer got %d events", n)
	}
}

// AUTH-041 / 10 filter matrix: role x event type.
func TestAUTH041_FilterMatrix(t *testing.T) {
	e := newEnv(t)
	e.s.db.Exec(`INSERT INTO namespace(id,slug,display_name,environment_tier,approver_group,created_at,updated_at) VALUES('ns_b','b','B','prod','b-appr','x','x')`)
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','va','a','viewer'),('r2','ga',NULL,'global_admin')`)
	p := func(id string, g ...string) *auth.Principal {
		return &auth.Principal{ID: id, Groups: g, AuthMethod: "oidc"}
	}
	viewer, other, admin, appr := p("u1", "va"), p("u2"), p("u3", "ga"), p("u4", "b-appr")
	cases := []struct {
		name string
		ev   events.Event
		want [4]bool // viewer-of-a, nobody, global_admin, b approver
	}{
		{"stack a", events.Event{Type: "stack.updated", Namespace: "a"}, [4]bool{true, false, true, false}},
		{"deployment b", events.Event{Type: "deployment.updated", Namespace: "b"}, [4]bool{false, false, true, false}},
		{"audit a", events.Event{Type: "audit.created", Namespace: "a"}, [4]bool{true, false, true, false}},
		{"audit global", events.Event{Type: "audit.created"}, [4]bool{false, false, true, false}},
		{"target", events.Event{Type: "target.status"}, [4]bool{true, false, true, false}},
		{"grant b by u2", events.Event{Type: "access_grant.updated", Namespace: "b", Requester: "u2"}, [4]bool{false, true, true, true}},
	}
	for _, c := range cases {
		for i, pr := range []*auth.Principal{viewer, other, admin, appr} {
			if got := e.s.visible(pr, c.ev); got != c.want[i] {
				t.Errorf("%s principal %d: got %v want %v", c.name, i, got, c.want[i])
			}
		}
	}
}
