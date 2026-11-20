package api

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/KazzyAPI/control-8/internal/audit"
	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/inventory"
	"github.com/KazzyAPI/control-8/internal/store"
	"github.com/KazzyAPI/control-8/internal/targets"
)

type server struct {
	db       *sql.DB
	cfg      *config.Config
	now      func() time.Time
	mu       sync.Mutex
	oidcProv *oidc.Provider
	// shortcut: in-memory per-IP failure log (lost on restart, keyed by RemoteAddr), fine for a single replica
	fails       map[string][]time.Time
	bus         *events.Bus
	targets     *targets.Checker
	tick        func(time.Duration) (<-chan time.Time, func()) // heartbeat clock (faked in tests)
	streams     map[string]int                                 // open SSE streams per principal (guarded by mu)
	nstreams    int
	drivers     DriverFunc
	progress    *events.Coalescer // deployment.updated rollout progress, 1/s (EVT-005)
	locks       sync.Map          // per-stack worker mutex (DOM-001)
	wg          sync.WaitGroup    // running deployment and webhook goroutines (tests wait on it)
	inv         *inventory.Engine
	hookBackoff []time.Duration // webhook retry waits; nil = 1s, 5s (GRT-041)
}

const cookieName = "c8_session"

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// decode enforces the 1 MiB body limit (413) and rejects unknown fields (400) (API-001).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			problem(w, 413, "body_too_large", "request body exceeds 1 MiB")
		} else {
			problem(w, 400, "invalid_body", err.Error())
		}
		return false
	}
	return true
}

func (s *server) audit(r *http.Request, p *auth.Principal, action, ttype, tid, outcome string, d map[string]any) {
	s.auditNS(r, p, "", action, ttype, tid, outcome, d)
}

// auditNS is audit with the namespace slug recorded in audit_event.namespace_id.
func (s *server) auditNS(r *http.Request, p *auth.Principal, ns, action, ttype, tid, outcome string, d map[string]any) {
	e := audit.Event{Namespace: ns, Action: action, TargetType: ttype, TargetID: tid, Outcome: outcome, Details: d, RequestID: r.Header.Get("X-Request-Id")}
	if p != nil {
		e.ActorType, e.ActorID, e.ActorDisplay, e.AuthMethod = "user", p.ID, p.Display, p.AuthMethod
		if p.IsToken() {
			e.ActorType = "token"
		}
	}
	if a, err := audit.Insert(s.db, e); err == nil {
		s.publish(events.Event{Type: "audit.created", At: s.now(), Namespace: ns, Data: a})
	}
}

// api wraps an /api/v1 handler: authenticate (401), CSRF for cookie writes (API-004), then run.
// Denials and every state-changing call are audited by the handler or here (DOM-009).
func (s *server) api(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(cookieName)
		var cookie string
		if c != nil {
			cookie = c.Value
		}
		p, err := auth.Authenticate(s.db, r.Header.Get("Authorization"), cookie, s.now())
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem(w, 401, "unauthenticated", "authentication required")
			return
		}
		if !p.IsToken() && r.Method != http.MethodGet && r.Method != http.MethodHead &&
			subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(p.CSRF)) != 1 {
			s.audit(r, p, "csrf.check", "", "", "denied", map[string]any{"path": r.URL.Path})
			problem(w, 403, "csrf_failed", "missing or invalid X-CSRF-Token")
			return
		}
		h(w, r, p)
	}
}

// deny maps an authz error to 404/403, audits the denial and writes the problem.
func (s *server) deny(w http.ResponseWriter, r *http.Request, p *auth.Principal, action string, err error) {
	s.denyNS(w, r, p, "", action, err)
}

func (s *server) denyNS(w http.ResponseWriter, r *http.Request, p *auth.Principal, ns, action string, err error) {
	s.auditNS(r, p, ns, action, "", "", "denied", nil)
	if errors.Is(err, authz.ErrNotFound) {
		problem(w, 404, "not_found", "not found")
		return
	}
	problem(w, 403, "forbidden", "insufficient role")
}

func (s *server) secureCookie() bool {
	u, _ := url.Parse(s.cfg.PublicURL)
	return !(u != nil && u.Scheme == "http" && u.Hostname() == "localhost")
}

const (
	bgWindow = 15 * time.Minute
	bgMax    = 5
)

// breakGlass: POST {"password"} (AUTH-010..013). Shortcut: no GET login form until the SPA (M13).
func (s *server) breakGlass(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BreakglassHash == "" {
		problem(w, 404, "not_found", "break-glass is disabled")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := s.now()
	s.mu.Lock()
	recent := s.fails[ip][:0:0]
	for _, t := range s.fails[ip] {
		if now.Sub(t) < bgWindow {
			recent = append(recent, t)
		}
	}
	s.fails[ip] = recent
	limited := len(recent) >= bgMax
	s.mu.Unlock()
	d := map[string]any{"ip": ip}
	if limited {
		s.audit(r, nil, "auth.break_glass", "user", "breakglass", "denied", d)
		problem(w, 429, "rate_limited", "too many failed attempts")
		return
	}
	var body struct{ Password string }
	if !decode(w, r, &body) {
		return
	}
	if !auth.VerifyPassword(s.cfg.BreakglassHash, body.Password) {
		s.mu.Lock()
		s.fails[ip] = append(s.fails[ip], now)
		s.mu.Unlock()
		s.audit(r, nil, "auth.break_glass", "user", "breakglass", "denied", d)
		problem(w, 401, "invalid_credentials", "invalid credentials")
		return
	}
	id := auth.NewID("usr")
	s.db.Exec(`INSERT INTO "user"(id,issuer,subject,display_name,is_break_glass,last_login_at) VALUES(?,?,?,?,1,?)
		ON CONFLICT(issuer,subject) DO UPDATE SET last_login_at=excluded.last_login_at`, id, "local", "breakglass", "Break-glass admin", store.Ts(now))
	s.db.QueryRow(`SELECT id FROM "user" WHERE issuer='local' AND subject='breakglass'`).Scan(&id)
	cookie, _, err := auth.NewSession(s.db, id, "break_glass", time.Hour, now)
	if err != nil {
		problem(w, 500, "internal", "session error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookie, Path: "/", HttpOnly: true, Secure: s.secureCookie(),
		SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	s.audit(r, &auth.Principal{ID: id, Display: "Break-glass admin", AuthMethod: "break_glass"}, "auth.break_glass", "user", id, "success", d)
	w.WriteHeader(204)
}

// logout deletes the session row (AUTH-005); CSRF applies like any cookie write.
func (s *server) logout(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if p.IsToken() {
		problem(w, 400, "no_session", "not a session")
		return
	}
	c, _ := r.Cookie(cookieName)
	auth.DeleteSession(s.db, c.Value)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1})
	s.audit(r, p, "auth.logout", "user", p.ID, "success", nil)
	w.WriteHeader(204)
}
