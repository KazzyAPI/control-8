// Package api holds HTTP handlers and middleware.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/inventory"
	"github.com/KazzyAPI/control-8/internal/targets"
)

// Option configures optional collaborators.
type Option func(*server)

// WithInventory publishes stack.updated whenever the engine changes a stack's observed state (K8S-061).
func WithInventory(e *inventory.Engine) Option {
	return func(s *server) {
		s.inv = e
		e.Changed = func(id string) {
			if x, err := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, id)); err == nil {
				s.publishStack(x)
			}
		}
	}
}

// New returns the root handler: health, /api/v1, /auth, request-id + access log (API-007).
// tc may be nil (no targets); its status changes are published as target.status.
// drivers may be nil (every deployment then fails target_unreachable).
func New(db *sql.DB, log *slog.Logger, cfg *config.Config, tc *targets.Checker, drivers DriverFunc, opts ...Option) http.Handler {
	mux := http.NewServeMux()
	s := &server{db: db, cfg: cfg, now: time.Now, fails: map[string][]time.Time{}, tick: realTick, streams: map[string]int{},
		bus: events.New(cfg.EventsReplay, time.Now()), targets: tc, drivers: drivers, progress: events.NewCoalescer(time.Second)}
	if tc != nil {
		tc.Publish = s.publishTarget
	}
	for _, o := range opts {
		o(s)
	}
	s.routes(mux)
	s.recoverDeployments()
	go s.sweepLoop()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
		if tc != nil { // targets are reported, never required (ARC-013)
			for _, t := range tc.List() {
				fmt.Fprintf(w, "target %s reachable=%t\n", t.Name, t.Reachable)
			}
		}
	})
	return requestID(accessLog(log, mux))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) Flush() { s.ResponseWriter.(http.Flusher).Flush() }

func (s *statusWriter) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			b := make([]byte, 10)
			rand.Read(b)
			id = "req_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
		}
		w.Header().Set("X-Request-Id", id)
		r.Header.Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

func accessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{w, 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		log.Info("access", "request_id", r.Header.Get("X-Request-Id"), "method", r.Method,
			"path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(start).Milliseconds())
	})
}
