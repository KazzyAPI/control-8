package api

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/store"
)

// UI-005, M16: CSP and browser protections on SPA, API, health and error responses.
func TestUI005_SecurityHeaders(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := &config.Config{PublicURL: "http://localhost:8000", EventsMaxConns: 5, EventsReplay: 10}
	h := New(db, slog.New(slog.NewJSONHandler(io.Discard, nil)), cfg, nil, nil)
	for _, p := range []string{"/", "/some/spa/route", "/healthz", "/api/v1/me", "/api/v1/nope", "/auth/nope"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		for k, v := range map[string]string{
			"Content-Security-Policy": "default-src 'self'; connect-src 'self'; frame-ancestors 'none'",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
			"X-Frame-Options":         "DENY",
		} {
			if got := w.Header().Get(k); got != v {
				t.Errorf("%s %s = %q", p, k, got)
			}
		}
	}
}

// AUTH-043, AUTH-030, K8S-001, M5, M16: no secret, token plaintext/hash, kubeconfig or deployment
// output in any response, event, log line or DB row.
func TestAUTH043_NoSecretsAnywhere(t *testing.T) {
	m := newM7(t)
	const (
		oidc, hook, reg, kc = "SENT-OIDC-SECRET", "SENT-WEBHOOK-SECRET", "SENT-REGISTRY-AUTH", "SENT-KUBECONFIG-CONTENT"
	)
	m.s.cfg.OIDCClientSecret, m.s.cfg.WebhookSecret, m.s.cfg.RegistryAuth = oidc, hook, reg
	plain, hash := auth.GenToken()
	addOwner(m.env, "sub1", `["ops"]`)
	syncTokens(t, m.env, auth.TokenDef{Name: "ci", OwnerSub: "sub1", Role: "viewer", SHA256: hash, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	bearer := map[string]string{"Authorization": "Bearer " + plain}
	m.s.db.Exec(`UPDATE target SET last_error=? WHERE name='home'`, "kubeconfig_invalid") // content is never stored; only the fixed text

	sub, _, _ := m.s.bus.Subscribe(nil)
	m.revision(cm)
	code, d := m.deploy(m.op, 1)
	m.s.wg.Wait()
	if code != 202 {
		t.Fatalf("deploy %d", code)
	}
	var out bytes.Buffer
	for _, p := range []string{"/api/v1/me", "/api/v1/tokens", "/api/v1/targets", "/api/v1/namespaces", "/api/v1/namespaces/a/stacks",
		"/api/v1/namespaces/a/stacks/s1", "/api/v1/namespaces/a/stacks/s1/revisions", "/api/v1/namespaces/a/stacks/s1/deployments",
		"/api/v1/role-bindings", "/api/v1/policies/global", "/api/v1/audit", "/api/v1/access-grants", "/api/v1/inventory"} {
		for _, h := range []map[string]string{m.admin, bearer} {
			out.WriteString(m.do("GET", p, "", h).Body.String())
		}
	}
	out.WriteString(m.do("GET", "/api/v1/deployments/"+d["id"].(string), "", m.admin).Body.String()) // REST may carry output

	// events: AUTH-043 payloads never carry deployment output
	var ev bytes.Buffer
	for len(sub.C) > 0 {
		e := <-sub.C
		fmt.Fprintf(&ev, "%s %+v\n", e.Type, e.Data)
	}
	if ev.Len() == 0 || strings.Contains(ev.String(), "applied") || strings.Contains(ev.String(), "output") {
		t.Fatalf("event payloads: %s", ev.String())
	}

	rows := dumpDB(t, m.s.db)
	sens := []string{oidc, hook, reg, kc, plain, bgPass, m.s.cfg.BreakglassHash}
	for name, text := range map[string]string{"responses": out.String(), "events": ev.String(), "log": m.log.String(), "db": rows} {
		for _, s := range sens {
			if strings.Contains(text, s) {
				t.Errorf("%s contain secret %q", name, s[:8])
			}
		}
	}
	// the token hash is stored (AUTH-031) but never returned or published
	for name, text := range map[string]string{"responses": out.String(), "events": ev.String(), "log": m.log.String()} {
		if strings.Contains(text, hash) {
			t.Errorf("%s contain the token hash", name)
		}
	}
	if !strings.Contains(rows, hash) {
		t.Error("sanity: token hash should be in DB")
	}
}

func dumpDB(t *testing.T, db *sql.DB) string {
	var b strings.Builder
	trs, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for trs.Next() {
		var n string
		trs.Scan(&n)
		tables = append(tables, n)
	}
	trs.Close()
	for _, tb := range tables {
		rs, err := db.Query(`SELECT * FROM "` + tb + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rs.Columns()
		for rs.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			rs.Scan(p...)
			for i, x := range v {
				if bs, ok := x.([]byte); ok {
					v[i] = string(bs)
				}
			}
			fmt.Fprintf(&b, "%s %v\n", tb, v)
		}
		rs.Close()
	}
	return b.String()
}
