package api

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
)

// user logs in a fake OIDC user with the given groups; returns cookie+csrf headers.
func (e *env) user(sub, groups string) map[string]string {
	e.s.db.Exec(`INSERT INTO "user"(id,issuer,subject,groups) VALUES(?,?,?,?)`, "usr_"+sub, "https://idp", sub, groups)
	cookie, csrf, _ := auth.NewSession(e.s.db, "usr_"+sub, "oidc", time.Hour, time.Now())
	return map[string]string{"Cookie": cookieName + "=" + cookie, "X-CSRF-Token": csrf}
}

func TestM2_NamespaceAssetMatrix(t *testing.T) { // API-005, DOM-009, GRT-001, GRT-003, GRT-027, D-50
	e := newEnv(t)
	admin, _ := e.login()
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES
		('r1','va','a','viewer'),('r2','oa','a','operator'),('r3','aa','a','admin')`)
	va, oa, aa, nobody := e.user("va", `["va"]`), e.user("oa", `["oa"]`), e.user("aa", `["aa"]`), e.user("no", `[]`)

	post := func(h map[string]string, path, body string) int { return e.do("POST", path, body, h).Code }
	if c := post(admin, "/api/v1/namespaces", `{"slug":"a","display_name":"A"}`); c != 400 { // no tier
		t.Fatalf("create without tier = %d", c)
	}
	if c := post(admin, "/api/v1/namespaces", `{"slug":"a","display_name":"A","environment_tier":"prod"}`); c != 400 {
		t.Fatalf("prod without approver_group = %d", c)
	}
	if c := post(va, "/api/v1/namespaces", `{"slug":"a","display_name":"A","environment_tier":"non-prod"}`); c != 403 {
		t.Fatalf("viewer create namespace = %d", c)
	}
	for _, b := range []string{`{"slug":"a","display_name":"A","environment_tier":"non-prod"}`, `{"slug":"b","display_name":"B","environment_tier":"prod","approver_group":"b-appr"}`} {
		if c := post(admin, "/api/v1/namespaces", b); c != 201 {
			t.Fatalf("create = %d", c)
		}
	}
	if c := post(admin, "/api/v1/namespaces", `{"slug":"a","display_name":"A","environment_tier":"non-prod"}`); c != 409 {
		t.Fatalf("duplicate = %d", c)
	}

	// role x endpoint: want status per caller (viewer, operator, admin-of-a, no-binding) for namespace a.
	asset := `{"slug":"web","name":"Web"}`
	cases := []struct {
		method, path, body string
		want               [4]int
	}{
		{"GET", "/api/v1/namespaces/a", "", [4]int{200, 200, 200, 404}},
		{"GET", "/api/v1/namespaces/b", "", [4]int{404, 404, 404, 404}},
		{"POST", "/api/v1/namespaces/a/assets", asset, [4]int{403, 201, 409, 404}},
		{"GET", "/api/v1/namespaces/a/assets/web", "", [4]int{200, 200, 200, 404}},
		{"PATCH", "/api/v1/namespaces/a/assets/web", `{"owner":"me"}`, [4]int{403, 200, 200, 404}},
		{"PATCH", "/api/v1/namespaces/a", `{"display_name":"AA"}`, [4]int{403, 403, 200, 404}},
		{"PATCH", "/api/v1/namespaces/a", `{"environment_tier":"prod"}`, [4]int{403, 403, 403, 404}},
		{"PATCH", "/api/v1/namespaces/a", `{"approver_group":"x"}`, [4]int{403, 403, 403, 404}},
		{"DELETE", "/api/v1/namespaces/a/assets/web", "", [4]int{403, 403, 204, 404}},
		{"DELETE", "/api/v1/namespaces/a", "", [4]int{403, 403, 403, 403}},
	}
	for _, c := range cases {
		for i, h := range []map[string]string{va, oa, aa, nobody} {
			if i == 2 && c.path == "/api/v1/namespaces/a/assets" && c.method == "POST" {
				e.do("POST", c.path, c.body, oa) // ensure it exists (operator ran before; admin hits 409)
			}
			if w := e.do(c.method, c.path, c.body, h); w.Code != c.want[i] {
				t.Errorf("%s %s as #%d = %d want %d: %s", c.method, c.path, i, w.Code, c.want[i], w.Body)
			}
		}
		// re-create the asset after the delete row so later rows still find it
		if c.method == "DELETE" && c.want[2] == 204 {
			e.do("POST", "/api/v1/namespaces/a/assets", asset, oa)
		}
	}
	if c := post(oa, "/api/v1/namespaces/a/assets", `{"slug":"x","name":"X","external_url":"https://x.example"}`); c != 400 {
		t.Fatalf("external_url on non-argo = %d", c)
	}
	if c := post(oa, "/api/v1/namespaces/a/assets", `{"slug":"argo1","name":"X","managed_by":"argo","external_url":"https://argo.example/app"}`); c != 201 {
		t.Fatalf("argo asset = %d", c)
	}

	// visibility: viewer of a lists only a; admin lists both
	var l struct{ Items []map[string]any }
	json.Unmarshal(e.do("GET", "/api/v1/namespaces", "", va).Body.Bytes(), &l)
	if len(l.Items) != 1 || l.Items[0]["slug"] != "a" {
		t.Fatalf("viewer list = %v", l.Items)
	}
	json.Unmarshal(e.do("GET", "/api/v1/namespaces", "", admin).Body.Bytes(), &l)
	if len(l.Items) != 2 {
		t.Fatalf("admin list = %v", l.Items)
	}

	// admin (global) changes tier + approver_group: allowed and audited; prod needs approver_group.
	if w := e.do("PATCH", "/api/v1/namespaces/a", `{"environment_tier":"prod"}`, admin); w.Code != 400 {
		t.Fatalf("to prod without group = %d", w.Code)
	}
	if w := e.do("PATCH", "/api/v1/namespaces/a", `{"environment_tier":"prod","approver_group":"a-appr"}`, admin); w.Code != 200 {
		t.Fatalf("tier change = %d %s", w.Code, w.Body)
	}
	if e.audits("namespace.tier_change", "success") != 1 || e.audits("namespace.update", "denied") == 0 {
		t.Fatal("tier change / denials not audited")
	}
	if w := e.do("DELETE", "/api/v1/namespaces/a", "", admin); w.Code != 409 {
		t.Fatalf("delete non-empty = %d", w.Code)
	}
	if w := e.do("DELETE", "/api/v1/namespaces/b", "", admin); w.Code != 204 {
		t.Fatalf("delete empty = %d", w.Code)
	}
	for _, a := range []string{"namespace.create", "asset.create", "asset.update", "asset.delete", "namespace.delete"} {
		if e.audits(a, "success") == 0 {
			t.Errorf("no success audit for %s", a)
		}
	}
}

func TestM2_AuditQuery(t *testing.T) { // API-006, API-005
	e := newEnv(t)
	admin, _ := e.login()
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','va','a','viewer')`)
	va := e.user("va", `["va"]`)
	for _, s := range []string{"a", "b"} {
		e.do("POST", "/api/v1/namespaces", fmt.Sprintf(`{"slug":%q,"display_name":"x","environment_tier":"non-prod"}`, s), admin)
	}
	type res struct {
		Items      []map[string]any
		NextCursor *string `json:"next_cursor"`
	}
	get := func(h map[string]string, q string) (int, res) {
		w := e.do("GET", "/api/v1/audit"+q, "", h)
		var r res
		json.Unmarshal(w.Body.Bytes(), &r)
		return w.Code, r
	}
	if c, r := get(va, ""); c != 200 || len(r.Items) != 1 {
		t.Fatalf("viewer audit of a: %d %v", c, r.Items)
	}
	e.do("POST", "/api/v1/namespaces/a/assets", `{"slug":"w","name":"W"}`, admin)
	if c, r := get(va, "?namespace=a"); c != 200 || len(r.Items) != 2 {
		t.Fatalf("viewer ns=a: %d %v", c, r.Items)
	}
	if c, _ := get(va, "?namespace=b"); c != 404 {
		t.Fatalf("viewer ns=b = %d", c)
	}
	if c, r := get(admin, "?action=namespace.create&limit=1"); c != 200 || len(r.Items) != 1 || r.NextCursor == nil {
		t.Fatalf("admin filter/limit: %d %v", c, r.Items)
	} else if _, r2 := get(admin, "?action=namespace.create&limit=1&cursor="+*r.NextCursor); len(r2.Items) != 1 || r2.NextCursor != nil || r2.Items[0]["target_id"] == r.Items[0]["target_id"] {
		t.Fatalf("cursor page = %v", r2.Items)
	}
	if _, r := get(admin, "?namespace=a&actor_id=nobody"); len(r.Items) != 0 {
		t.Fatal("actor filter")
	}
	if _, r := get(admin, "?since=2999-01-01T00:00:00Z"); len(r.Items) != 0 {
		t.Fatal("since filter")
	}
	if c, _ := get(admin, "?grant_id=agr_x"); c != 200 {
		t.Fatalf("grant_id filter = %d", c)
	}
	if c, _ := get(admin, "?limit=999"); c != 400 {
		t.Fatalf("limit cap = %d", c)
	}
}
