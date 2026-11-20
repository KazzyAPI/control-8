package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/events"
)

const maxStreamsPerPrincipal = 5 // EVT-013

func (s *server) publish(e events.Event) {
	if s.bus != nil {
		s.bus.Publish(e)
	}
}

// visible is the per-viewer filter (10 table, AUTH-041). Roles are read fresh per event (AUTH-042).
func (s *server) visible(p *auth.Principal, e events.Event) bool {
	switch e.Type {
	case "audit.created":
		if e.Namespace == "" {
			return authz.Effective(s.db, p, "") == 4
		}
	case "target.status":
		return s.anyRole(p)
	case "access_grant.updated":
		return p.ID == e.Requester || authz.Effective(s.db, p, "") == 4 || slices.Contains(s.approverFor(p.Groups), e.Namespace)
	}
	return authz.Effective(s.db, p, e.Namespace) >= 1
}

// anyRole: a role in at least one namespace (a binding with no namespace counts for every one).
func (s *server) anyRole(p *auth.Principal) bool {
	if authz.Effective(s.db, p, "\x00") > 0 {
		return true
	}
	rows, err := s.db.Query(`SELECT DISTINCT namespace_id FROM role_binding WHERE namespace_id IS NOT NULL`)
	if err != nil {
		return false
	}
	var slugs []string // collected first: the DB pool has one connection
	for rows.Next() {
		var n string
		rows.Scan(&n)
		slugs = append(slugs, n)
	}
	rows.Close()
	return slices.ContainsFunc(slugs, func(n string) bool { return authz.Effective(s.db, p, n) > 0 })
}

func (s *server) acquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams[id] >= maxStreamsPerPrincipal || s.nstreams >= s.cfg.EventsMaxConns {
		return false
	}
	s.streams[id]++
	s.nstreams++
	return true
}

func (s *server) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams[id]--; s.streams[id] == 0 {
		delete(s.streams, id)
	}
	s.nstreams--
}

func send(w http.ResponseWriter, id uint64, typ string, e events.Event) {
	b, _ := json.Marshal(e)
	if id != 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
	w.(http.Flusher).Flush()
}

// stream serves GET /api/v1/events (EVT-010..015). Credentials are re-checked on every heartbeat (EVT-014).
func (s *server) stream(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.acquire(p.ID) {
		problem(w, 429, "too_many_streams", "too many event streams")
		return
	}
	defer s.release(p.ID)
	q := r.URL.Query()
	var types []string
	if t := q.Get("types"); t != "" {
		types = strings.Split(t, ",")
	}
	ns := q.Get("namespace")
	var last *uint64
	lid := r.Header.Get("Last-Event-ID")
	if lid == "" {
		lid = q.Get("last_event_id")
	}
	if lid != "" {
		n, err := strconv.ParseUint(lid, 10, 64)
		if err != nil {
			n = 0 // unparsable ⇒ unknown epoch ⇒ reset
		}
		last = &n
	}
	sub, replay, reset := s.bus.Subscribe(last)
	defer s.bus.Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fmt.Fprint(w, "retry: 3000\n\n")
	w.(http.Flusher).Flush()
	emit := func(e events.Event) {
		if (len(types) == 0 || slices.Contains(types, e.Type)) && (ns == "" || e.Namespace == ns) && s.visible(p, e) {
			send(w, e.ID, e.Type, e)
		}
	}
	if reset {
		send(w, 0, "reset", events.Event{Type: "reset", At: s.now(), Data: map[string]string{"reason": "resume_unavailable"}})
	}
	for _, e := range replay {
		emit(e)
	}
	tick, stop := s.tick(s.cfg.EventsHeartbeat)
	defer stop()
	c, _ := r.Cookie(cookieName)
	var cookie string
	if c != nil {
		cookie = c.Value
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return // dropped as a slow consumer; client resumes (EVT-001)
			}
			emit(e)
		case <-tick:
			if _, err := auth.Authenticate(s.db, r.Header.Get("Authorization"), cookie, s.now()); err != nil {
				send(w, 0, "session.ended", events.Event{Type: "session.ended", At: s.now(), Data: map[string]string{"reason": "unauthenticated"}})
				return
			}
			fmt.Fprint(w, ": ping\n\n")
			w.(http.Flusher).Flush()
		}
	}
}

func realTick(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}
