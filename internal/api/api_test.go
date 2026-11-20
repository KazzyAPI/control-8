package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/store"
)

func TestHealthReadyRequestID(t *testing.T) { // API-007
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	h := New(db, slog.New(slog.NewJSONHandler(&buf, nil)), &config.Config{PublicURL: "http://localhost:8000"}, nil, nil)
	for _, p := range []string{"/healthz", "/readyz", "/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if !strings.HasPrefix(rec.Header().Get("X-Request-Id"), "req_") {
			t.Fatalf("%s: missing request id", p)
		}
		if p != "/nope" && rec.Code != 200 || p == "/nope" && rec.Code != 404 {
			t.Fatalf("%s: status %d", p, rec.Code)
		}
	}
	if !strings.Contains(buf.String(), `"request_id"`) {
		t.Fatal("access log lacks request_id")
	}
	db.Close()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with closed db = %d", rec.Code)
	}
}
