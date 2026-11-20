package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oauth2-proxy/mockoidc"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/store"
)

// oidcLogin drives login -> mock authorize -> callback without following redirects; tamper rewrites state.
func oidcFlow(t *testing.T, e *env, m *mockoidc.MockOIDC, tamper bool) *httptest.ResponseRecorder {
	t.Helper()
	w := e.do("GET", "/auth/login", "", nil)
	if w.Code != 302 {
		t.Fatalf("login = %d %s", w.Code, w.Body)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(w.Header().Get("Location"))
	if err != nil || resp.StatusCode != 302 {
		t.Fatalf("authorize: %v %v", err, resp)
	}
	cb := resp.Header.Get("Location") // http://localhost:8000/auth/callback?code=..&state=..
	u, _ := http.NewRequest("GET", cb, nil)
	q := u.URL.Query()
	if tamper {
		q.Set("state", "evil")
	}
	r := httptest.NewRequest("GET", "/auth/callback?"+q.Encode(), nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func TestM3_OIDCLoginGroupsTokens(t *testing.T) { // AUTH-001..006, AUTH-003, AUTH-004, AUTH-023, AUTH-032
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown()
	e := newEnv(t)
	e.s.cfg.OIDCIssuer, e.s.cfg.OIDCClientID, e.s.cfg.OIDCClientSecret, e.s.cfg.OIDCGroupsClaim = m.Issuer(), m.Config().ClientID, m.Config().ClientSecret, "groups"
	e.s.db.Exec(`INSERT INTO namespace(id,slug,display_name,environment_tier,approver_group,created_at,updated_at) VALUES('ns_1','shop','Shop','prod','shop-approvers','x','x')`)
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','shop-ops','shop','operator')`)

	login := func(groups ...string) *httptest.ResponseRecorder {
		m.QueueUser(&mockoidc.MockUser{Subject: "sub-1", Email: "a@x", Groups: groups})
		return oidcFlow(t, e, m, false)
	}
	me := func(rec *httptest.ResponseRecorder) (m2 map[string]any) {
		h := map[string]string{}
		for _, c := range rec.Result().Cookies() {
			if c.Name == cookieName {
				h["Cookie"] = c.Name + "=" + c.Value
			}
		}
		json.Unmarshal(e.do("GET", "/api/v1/me", "", h).Body.Bytes(), &m2)
		return
	}

	// approver group member: session, approver_for, no role binding but not sent to /no-access
	rec := login("shop-approvers")
	if rec.Code != 302 || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if got := me(rec)["approver_for"]; len(got.([]any)) != 1 || got.([]any)[0] != "shop" {
		t.Fatalf("approver_for = %v", got)
	}
	if me(rec)["auth_method"] != "oidc" || e.audits("auth.login", "success") != 1 {
		t.Fatal("expected oidc session + audit")
	}

	// groups are recomputed at each login (AUTH-004): now an operator of shop only
	rec = login("shop-ops")
	if ns := me(rec)["namespaces"].([]any); len(ns) != 1 || ns[0].(map[string]any)["role"] != "operator" {
		t.Fatalf("namespaces = %v", ns)
	}
	if got := me(rec)["approver_for"]; len(got.([]any)) != 0 {
		t.Fatalf("approver_for = %v", got)
	}
	var n int
	e.s.db.QueryRow(`SELECT count(*) FROM "user" WHERE subject='sub-1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("users = %d", n)
	}

	shopNS := "shop"
	// token owned by sub-1 acts with that user's role (AUTH-032)
	syncTokens(t, e, auth.TokenDef{Name: "ci", OwnerSub: "sub-1", Namespace: &shopNS, Role: "admin", SHA256: auth.HashToken("c8_x"), ExpiresAt: store.Ts(time.Now().Add(time.Hour))})
	tr := e.do("GET", "/api/v1/me", "", map[string]string{"Authorization": "Bearer c8_x"})
	var tm map[string]any
	json.Unmarshal(tr.Body.Bytes(), &tm)
	if tr.Code != 200 || tm["namespaces"].([]any)[0].(map[string]any)["role"] != "operator" {
		t.Fatalf("token me = %d %v", tr.Code, tm)
	}

	// no bindings -> /no-access
	if rec = login("nobody"); rec.Header().Get("Location") != "/no-access" {
		t.Fatalf("no-access redirect = %s", rec.Header().Get("Location"))
	}
	if e.do("GET", "/no-access", "", nil).Code != 200 || e.do("GET", "/login", "", nil).Code != 200 {
		t.Fatal("pages")
	}

	// tampered state -> 400 (AUTH-002), audited
	m.QueueUser(&mockoidc.MockUser{Subject: "sub-1", Groups: []string{"shop-ops"}})
	if rec = oidcFlow(t, e, m, true); rec.Code != 400 {
		t.Fatalf("tampered state = %d", rec.Code)
	}
	if e.audits("auth.login", "denied") != 1 {
		t.Fatal("tampered state not audited")
	}
}

func TestM3_BootstrapAdmin(t *testing.T) { // AUTH-021
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		if err := auth.Bootstrap(e.s.db, "root"); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	e.s.db.QueryRow(`SELECT count(*) FROM role_binding WHERE idp_group='root' AND role='global_admin' AND namespace_id IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatalf("bindings = %d", n)
	}
}
