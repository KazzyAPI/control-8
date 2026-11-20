// Package api holds HTTP handlers and middleware.
package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// New returns the root handler: /healthz, /readyz, request-id + access log (API-007).
func New(db *sql.DB, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	return requestID(accessLog(log, mux))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

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
