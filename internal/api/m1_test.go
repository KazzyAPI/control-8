package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/config"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/store"
)

const bgPass = "correct horse"

type env struct {
	t   *testing.T
	s   *server
	h   http.Handler
	log *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	buf := &bytes.Buffer{}
	cfg := &config.Config{PublicURL: "http://localhost:8000", BreakglassHash: auth.HashPassword(bgPass)}
	cfg.EventsMaxConns = 500
	s := &server{db: db, cfg: cfg, now: time.Now, fails: map[string][]time.Time{}, streams: map[string]int{}, bus: events.New(100, time.Now()),
		tick: func(time.Duration) (<-chan time.Time, func()) { return make(chan time.Time), func() {} }}
	mux := http.NewServeMux()
	s.routes(mux)
	return &env{t, s, requestID(accessLog(slog.New(slog.NewJSONHandler(buf, nil)), mux)), buf}
}

func (e *env) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "10.0.0.1:1234"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// login performs break-glass login and returns cookie header + csrf token.
func (e *env) login() (map[string]string, string) {
	w := e.do("POST", "/auth/break-glass", `{"password":"`+bgPass+`"}`, nil)
	if w.Code != 204 {
		e.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	ck := w.Result().Cookies()[0]
	h := map[string]string{"Cookie": ck.Name + "=" + ck.Value}
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(e.do("GET", "/api/v1/me", "", h).Body.Bytes(), &me)
	h["X-CSRF-Token"] = me.CSRF
	return h, me.CSRF
}

func (e *env) audits(action, outcome string) int {
	var n int
	e.s.db.QueryRow(`SELECT count(*) FROM audit_event WHERE action=? AND outcome=?`, action, outcome).Scan(&n)
	return n
}

func TestAUTH010_BreakGlassLoginMe(t *testing.T) { // AUTH-010, AUTH-011, AUTH-012, DOM-009
	e := newEnv(t)
	if w := e.do("POST", "/auth/break-glass", `{"password":"nope"}`, nil); w.Code != 401 {
		t.Fatalf("bad password: %d", w.Code)
	}
	h, csrf := e.login()
	var me map[string]any
	json.Unmarshal(e.do("GET", "/api/v1/me", "", h).Body.Bytes(), &me)
	if me["global_admin"] != true || me["auth_method"] != "break_glass" || csrf == "" {
		t.Fatalf("me = %v", me)
	}
	if e.audits("auth.break_glass", "denied") != 1 || e.audits("auth.break_glass", "success") != 1 {
		t.Fatal("login attempts not audited")
	}
	e.s.cfg.BreakglassHash = ""
	if w := e.do("POST", "/auth/break-glass", `{"password":"x"}`, nil); w.Code != 404 {
		t.Fatalf("disabled break-glass: %d", w.Code)
	}
}

func TestAUTH013_BreakGlassRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.do("POST", "/auth/break-glass", `{"password":"x"}`, nil)
	}
	if w := e.do("POST", "/auth/break-glass", `{"password":"`+bgPass+`"}`, nil); w.Code != 429 {
		t.Fatalf("got %d, want 429", w.Code)
	}
}

func TestAUTH015_Argon2(t *testing.T) {
	h := auth.HashPassword("pw")
	if !strings.HasPrefix(h, "$argon2id$v=19$") || !auth.VerifyPassword(h, "pw") || auth.VerifyPassword(h, "pW") || auth.VerifyPassword("garbage", "pw") {
		t.Fatal("argon2 verify")
	}
}

func TestAPI004_CSRF(t *testing.T) {
	e := newEnv(t)
	h, _ := e.login()
	body := `{"idp_group":"g","role":"viewer"}`
	bad := map[string]string{"Cookie": h["Cookie"]}
	if w := e.do("POST", "/api/v1/role-bindings", body, bad); w.Code != 403 || !strings.Contains(w.Body.String(), "csrf_failed") {
		t.Fatalf("no csrf: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/v1/role-bindings", body, h); w.Code != 201 {
		t.Fatalf("with csrf: %d %s", w.Code, w.Body)
	}
}

func TestAPI001_003_BodyAndProblem(t *testing.T) {
	e := newEnv(t)
	h, _ := e.login()
	w := e.do("POST", "/api/v1/role-bindings", `{"idp_group":"g","role":"viewer","extra":1}`, h)
	if w.Code != 400 || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("unknown field: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var p map[string]any
	json.Unmarshal(w.Body.Bytes(), &p)
	for _, k := range []string{"type", "title", "status", "detail", "code"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("problem missing %s", k)
		}
	}
	big := `{"idp_group":"` + strings.Repeat("a", 1<<20) + `","role":"viewer"}`
	if w := e.do("POST", "/api/v1/role-bindings", big, h); w.Code != 413 {
		t.Fatalf("big body: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/me", "", nil); w.Code != 401 {
		t.Fatalf("unauth: %d", w.Code)
	}
}

func TestAUTH020_RoleResolution(t *testing.T) { // table over bindings -> effective rank
	e := newEnv(t)
	db := e.s.db
	db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES
		('1','a','shop','viewer'),('2','b','shop','admin'),('3','c',NULL,'operator'),('4','ga',NULL,'global_admin'),('5','gx','shop','global_admin')`)
	cases := []struct {
		groups []string
		ns     string
		want   int
	}{
		{[]string{"a"}, "shop", 1},
		{[]string{"a", "b"}, "shop", 3}, // highest wins
		{[]string{"a"}, "other", 0},
		{[]string{"c"}, "other", 2}, // null namespace, non-global role: every namespace
		{[]string{"c"}, "", 0},
		{[]string{"ga"}, "other", 4},
		{[]string{"ga"}, "", 4},
		{[]string{"gx"}, "shop", 0}, // global_admin only counts with null namespace
		{nil, "shop", 0},
	}
	for _, c := range cases {
		if got := authz.Effective(db, &auth.Principal{Groups: c.groups}, c.ns); got != c.want {
			t.Errorf("groups=%v ns=%q got %d want %d", c.groups, c.ns, got, c.want)
		}
	}
}

func addOwner(e *env, sub, groups string) {
	e.s.db.Exec(`INSERT INTO "user"(id,issuer,subject,groups) VALUES(?,?,?,?)`, "usr_"+sub, "https://idp", sub, groups)
}

func syncTokens(t *testing.T, e *env, defs ...auth.TokenDef) {
	if err := auth.SyncTokens(e.s.db, defs, time.Now(), slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
}

func TestAUTH030_035_Tokens(t *testing.T) {
	e := newEnv(t)
	plain, hash := auth.GenToken()
	if !regexp.MustCompile(`^c8_[A-Z2-7]{52}$`).MatchString(plain) || auth.HashToken(plain) != hash {
		t.Fatalf("gen-token format: %s", plain)
	}
	addOwner(e, "sub1", `["ops"]`)
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','ops',NULL,'global_admin')`)
	def := auth.TokenDef{Name: "ci", OwnerSub: "sub1", Role: "admin", SHA256: hash, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	syncTokens(t, e, def)
	bearer := map[string]string{"Authorization": "Bearer " + plain}

	// authenticates; role = min(token admin, owner global_admin) -> not global admin (AUTH-032)
	w := e.do("GET", "/api/v1/me", "", bearer)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"global_admin":true`) {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	// AUTH-034: tokens cannot manage role bindings; denial audited
	if w := e.do("POST", "/api/v1/role-bindings", `{"idp_group":"g","role":"viewer"}`, bearer); w.Code != 403 || !strings.Contains(w.Body.String(), "token_not_allowed") {
		t.Fatalf("token write: %d %s", w.Code, w.Body)
	}
	if e.audits("role_binding.create", "denied") != 1 {
		t.Fatal("denied call not audited")
	}
	// AUTH-035: header only
	if w := e.do("GET", "/api/v1/me?token="+plain, "", nil); w.Code != 401 {
		t.Fatalf("query token: %d", w.Code)
	}
	// listing: no hash, no plaintext (AUTH-033)
	w = e.do("GET", "/api/v1/tokens", "", bearer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ci"`) || strings.Contains(w.Body.String(), hash) || strings.Contains(w.Body.String(), plain) {
		t.Fatalf("tokens: %d %s", w.Code, w.Body)
	}
	// removed from config -> revoked -> 401; sync audited
	syncTokens(t, e)
	if w := e.do("GET", "/api/v1/me", "", bearer); w.Code != 401 {
		t.Fatalf("revoked: %d", w.Code)
	}
	if e.audits("token.config_sync", "success") != 2 {
		t.Fatal("token.config_sync not audited")
	}
	// expired -> 401
	def.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	syncTokens(t, e, def)
	if w := e.do("GET", "/api/v1/me", "", bearer); w.Code != 401 {
		t.Fatalf("expired: %d", w.Code)
	}
	// unresolved owner -> 401
	def.ExpiresAt, def.OwnerSub = time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "ghost"
	syncTokens(t, e, def)
	if w := e.do("GET", "/api/v1/me", "", bearer); w.Code != 401 {
		t.Fatalf("unknown owner: %d", w.Code)
	}
	if strings.Contains(e.log.String(), plain) || strings.Contains(e.log.String(), hash) {
		t.Fatal("token material in logs")
	}
}

func TestAUTH031_ParseTokens(t *testing.T) {
	_, h := auth.GenToken()
	_, h2 := auth.GenToken()
	now := time.Now()
	exp := now.Add(time.Hour).UTC().Format(time.RFC3339)
	ok := `{"name":"a","owner_sub":"s","namespace":null,"role":"viewer","sha256":"` + h + `","expires_at":"` + exp + `"}`
	bad := map[string]string{
		"unknown field": strings.Replace(ok, `"name":"a"`, `"name":"a","x":1`, 1),
		"missing name":  strings.Replace(ok, `"name":"a"`, `"name":""`, 1),
		"bad role":      strings.Replace(ok, "viewer", "root", 1),
		"bad hash":      strings.Replace(ok, h, "abc", 1),
		"too far":       strings.Replace(ok, exp, now.AddDate(2, 0, 0).UTC().Format(time.RFC3339), 1),
		"dup name":      ok + "," + strings.Replace(ok, h, h2, 1),
		"dup hash":      ok + "," + strings.Replace(ok, `"name":"a"`, `"name":"b"`, 1),
	}
	parse := func(s string) error {
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte("["+s+"]"), &raw); err != nil {
			t.Fatal(err)
		}
		_, err := auth.ParseTokens(raw, now)
		return err
	}
	if err := parse(ok); err != nil {
		t.Fatal(err)
	}
	for name, s := range bad {
		if err := parse(s); err == nil || strings.Contains(err.Error(), h) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestDOM008_AuditAppendOnly(t *testing.T) {
	e := newEnv(t)
	e.login()
	if _, err := e.s.db.Exec(`UPDATE audit_event SET action='x'`); err == nil {
		t.Fatal("UPDATE on audit_event succeeded")
	}
	if _, err := e.s.db.Exec(`DELETE FROM audit_event`); err == nil {
		t.Fatal("DELETE on audit_event succeeded")
	}
}

func TestRoleBindingsAPI(t *testing.T) { // AUTH-020, AUTH-022, DOM-009
	e := newEnv(t)
	// anonymous and non-admin sessions
	addOwner(e, "u", `["viewers"]`)
	cookie, _, _ := auth.NewSession(e.s.db, "usr_u", "oidc", time.Hour, time.Now())
	nonAdmin := map[string]string{"Cookie": "c8_session=" + cookie}
	if w := e.do("GET", "/api/v1/role-bindings", "", nonAdmin); w.Code != 403 {
		t.Fatalf("non-admin list: %d", w.Code)
	}
	h, _ := e.login()
	w := e.do("POST", "/api/v1/role-bindings", `{"idp_group":"shop-devs","namespace":"shop","role":"operator"}`, h)
	var b binding
	json.Unmarshal(w.Body.Bytes(), &b)
	if w.Code != 201 || !strings.HasPrefix(b.ID, "rb_") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"idp_group":"g","role":"root"}`, `{"idp_group":"","role":"viewer"}`, `{"idp_group":"g","namespace":"Bad Slug","role":"viewer"}`, `{"idp_group":"g","namespace":"shop","role":"global_admin"}`} {
		if w := e.do("POST", "/api/v1/role-bindings", bad, h); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	if w := e.do("GET", "/api/v1/role-bindings", "", h); !strings.Contains(w.Body.String(), "shop-devs") {
		t.Fatalf("list: %s", w.Body)
	}
	var me struct{ Namespaces []map[string]string }
	json.Unmarshal(e.do("GET", "/api/v1/me", "", h).Body.Bytes(), &me)
	if len(me.Namespaces) != 1 || me.Namespaces[0]["role"] != "global_admin" {
		t.Fatalf("me namespaces: %v", me.Namespaces)
	}
	if w := e.do("DELETE", "/api/v1/role-bindings/"+b.ID, "", h); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/v1/role-bindings/"+b.ID, "", h); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}
	if e.audits("role_binding.create", "success") != 1 || e.audits("role_binding.delete", "success") != 1 || e.audits("role_binding.list", "denied") != 1 {
		t.Fatal("audit rows missing")
	}
	// logout kills the session
	if w := e.do("POST", "/auth/logout", "", h); w.Code != 204 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := e.do("GET", "/api/v1/me", "", h); w.Code != 401 {
		t.Fatalf("after logout: %d", w.Code)
	}
}

// API-002: every registered /api/v1 route is in openapi.yaml and vice versa; x-milestone paths are skipped.
func TestAPI002_OpenAPIParity(t *testing.T) {
	raw, err := os.ReadFile("../../docs/specs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := map[string]bool{}
	var path string
	skip := false
	inPaths := false
	for _, l := range strings.Split(string(raw), "\n") {
		switch {
		case l == "paths:":
			inPaths = true
		case inPaths && l != "" && l[0] != ' ' && l[0] != '#':
			inPaths = false
		case !inPaths:
		case regexp.MustCompile(`^  /\S*:$`).MatchString(l):
			path, skip = strings.TrimSuffix(strings.TrimSpace(l), ":"), false
		case strings.HasPrefix(l, "    x-milestone:"):
			skip = true // marker sits right under the path line, before any method
		}
		if m := regexp.MustCompile(`^    (get|post|put|patch|delete):`).FindStringSubmatch(l); inPaths && m != nil && !skip {
			spec[strings.ToUpper(m[1])+" /api/v1"+path] = true
		}
	}
	s := &server{}
	var routes []string
	for _, p := range s.routes(http.NewServeMux()) {
		if strings.Contains(p, " /api/v1/") {
			routes = append(routes, p)
		}
	}
	for _, r := range routes {
		if !spec[r] {
			t.Errorf("route %s missing from openapi.yaml", r)
		}
	}
	for m := range spec {
		if !slices.Contains(routes, m) {
			t.Errorf("openapi operation %s has no route (mark with x-milestone?)", m)
		}
	}
}
