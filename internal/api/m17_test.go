package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

func jm(w *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

type prd struct {
	*env
	m7                                                           *m7
	admin                                                        map[string]string
	drv                                                          *fakeDriver
	sop, sopa, sadm, sapr, oapr, pnsapr, bnsapr, pop, both, none map[string]string
}

func (p *prd) must(want int, method, path, body string, h map[string]string) map[string]any {
	p.t.Helper()
	w := p.do(method, path, body, h)
	if w.Code != want {
		p.t.Fatalf("%s %s = %d want %d: %s", method, path, w.Code, want, w.Body)
	}
	return jm(w)
}

func (p *prd) bind(id, group, ns, prodSlug, role string) {
	var pid, nsv any
	if prodSlug != "" {
		p.s.db.QueryRow(`SELECT id FROM product WHERE slug=?`, prodSlug).Scan(&pid)
	}
	if ns != "" {
		nsv = ns
	}
	p.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,product_id,role) VALUES(?,?,?,?,?)`, id, group, nsv, pid, role)
}

// newPRD: products shop (group shop-apr) and other (oth-apr); namespaces a, b, c (non-prod), d (unrelated), p (prod,
// namespace group p-ns-apr); shop environments dev@a, qa@c (non-prod), staging@b (prod tier on a non-prod
// namespace), prod@p; other prod@p; apps shop/api, other/worker, legacy unassigned/web (stack in p).
func newPRD(t *testing.T) *prd {
	e := newEnv(t)
	admin, _ := e.login()
	p := &prd{env: e, admin: admin, drv: &fakeDriver{}}
	e.s.cfg.GrantMax = 4 * time.Hour
	e.s.drivers = func(string) (rt.Driver, error) { return p.drv, nil }
	e.s.db.Exec(`INSERT INTO target(name,id,kind,environment_tier) VALUES('home','tgt_1','kubernetes','non-prod')`)
	for _, ns := range []string{"a", "b", "c", "d"} {
		p.must(201, "POST", "/api/v1/namespaces", `{"slug":"`+ns+`","display_name":"x","environment_tier":"non-prod"}`, admin)
	}
	p.must(201, "POST", "/api/v1/namespaces", `{"slug":"p","display_name":"x","environment_tier":"prod","approver_group":"p-ns-apr"}`, admin)
	p.must(201, "POST", "/api/v1/products", `{"slug":"shop","name":"Shop","approver_group":"shop-apr","tags":["retail"]}`, admin)
	p.must(201, "POST", "/api/v1/products", `{"slug":"other","name":"Other","approver_group":"oth-apr"}`, admin)
	env := func(prod, name, tier, ns string) {
		b := `{"name":"` + name + `","tier":"` + tier + `","target":"home","namespace":"` + ns + `"`
		if tier == "prod" {
			b += `,"confirm_prod":"` + prod + `"`
		}
		p.must(201, "POST", "/api/v1/products/"+prod+"/environments", b+`}`, admin)
	}
	env("shop", "dev", "non-prod", "a")
	env("shop", "qa", "non-prod", "c")
	env("shop", "staging", "prod", "b")
	env("shop", "prod", "prod", "p")
	env("other", "prod", "prod", "p")
	p.must(201, "POST", "/api/v1/products/shop/applications", `{"slug":"api","name":"API"}`, admin)
	p.must(201, "POST", "/api/v1/products/other/applications", `{"slug":"worker","name":"Worker"}`, admin)
	p.must(201, "POST", "/api/v1/namespaces/p/assets", `{"slug":"web","name":"Web"}`, admin)
	p.bind("rb_sop", "sop", "", "shop", "operator")
	p.bind("rb_sadm", "sadm", "", "shop", "admin")
	p.bind("rb_oop", "oop", "", "other", "operator")
	p.bind("rb_pop", "pop", "p", "", "operator")
	p.sop, p.sopa, p.sadm = e.user("sop", `["sop"]`), e.user("sopa", `["sop","shop-apr"]`), e.user("sadm", `["sadm"]`)
	p.sapr, p.oapr, p.pnsapr, p.bnsapr = e.user("sapr", `["shop-apr"]`), e.user("oapr", `["oth-apr"]`), e.user("pnsapr", `["p-ns-apr"]`), e.user("bnsapr", `["b-ns-apr"]`)
	p.pop, p.both, p.none = e.user("pop", `["pop"]`), e.user("both", `["sop","oop"]`), e.user("none", `[]`)
	// a product operator places apps in four environments without any namespace binding (PRD-044, PRD-012)
	for _, env := range []string{"dev", "qa", "staging", "prod"} {
		p.must(201, "POST", "/api/v1/products/shop/applications/api/stacks", `{"environment":"`+env+`"}`, p.sop)
	}
	p.must(201, "POST", "/api/v1/products/other/applications/worker/stacks", `{"environment":"prod"}`, admin)
	p.must(201, "POST", "/api/v1/namespaces/p/stacks", `{"slug":"web","asset":"web","target":"home"}`, p.pop)
	for _, s := range [][2]string{{"a", "api"}, {"b", "api"}, {"c", "api"}, {"p", "api"}, {"p", "worker"}, {"p", "web"}} {
		who := p.sop
		if s[1] == "worker" {
			who = admin
		} else if s[1] == "web" {
			who = p.pop
		}
		b, _ := json.Marshal(map[string]any{"safety_profile": "as_is", "manifests": cm})
		p.must(201, "POST", "/api/v1/namespaces/"+s[0]+"/stacks/"+s[1]+"/revisions", string(b), who)
	}
	return p
}

// AUTH-024, PRD-010, PRD-011, PRD-014: binding kind x resource kind x role, highest wins, no cross-grants.
func TestAUTH024_PRD011_PrecedenceMatrix(t *testing.T) {
	p := newPRD(t)
	p.bind("m_pv", "pv", "", "shop", "viewer")
	p.bind("m_po", "po", "", "shop", "operator")
	p.bind("m_pa", "pa", "", "shop", "admin")
	p.bind("m_nv", "nv", "a", "", "viewer")
	p.bind("m_no", "no", "a", "", "operator")
	p.bind("m_na", "na", "a", "", "admin")
	p.bind("m_gv", "gv", "", "", "viewer")
	p.bind("m_go", "go", "", "", "operator")
	p.bind("m_ga", "ga", "", "", "global_admin")
	p.bind("m_do", "do", "d", "", "operator")
	who := []string{"pv", "po", "pa", "nv", "no", "na", "gv", "go", "ga", "x", "d-op"}
	hs := map[string]map[string]string{}
	for _, g := range who {
		grp := g
		if g == "x" {
			grp = "unbound"
		} else if g == "d-op" {
			grp = "do"
		}
		hs[g] = p.user("m-"+g, `["`+grp+`"]`)
	}
	cases := []struct {
		name, method, path, body string
		want                     []int // in `who` order
	}{
		{"stack read", "GET", "/api/v1/namespaces/a/stacks/api", "", []int{200, 200, 200, 200, 200, 200, 200, 200, 200, 404, 404}},
		{"stack write", "POST", "/api/v1/namespaces/a/stacks/api/revisions", `{}`, []int{403, 400, 400, 403, 400, 400, 403, 400, 400, 404, 404}},
		{"namespace read", "GET", "/api/v1/namespaces/a", "", []int{404, 404, 404, 200, 200, 200, 200, 200, 200, 404, 404}},
		{"namespace write", "PATCH", "/api/v1/namespaces/a", `{"display_name":"A2"}`, []int{404, 404, 404, 403, 403, 200, 403, 403, 200, 404, 404}},
		{"product read", "GET", "/api/v1/products/shop", "", []int{200, 200, 200, 200, 200, 200, 200, 200, 200, 404, 404}},
		{"product write", "PATCH", "/api/v1/products/shop", `{"description":"d"}`, []int{403, 403, 200, 403, 403, 403, 403, 403, 200, 404, 404}},
		{"application read", "GET", "/api/v1/products/shop/applications/api", "", []int{200, 200, 200, 200, 200, 200, 200, 200, 200, 404, 404}},
		{"application write", "PATCH", "/api/v1/products/shop/applications/api", `{"owner":"me"}`, []int{403, 200, 200, 403, 403, 403, 403, 200, 200, 404, 404}},
	}
	for _, c := range cases {
		for i, g := range who {
			if w := p.do(c.method, c.path, c.body, hs[g]); w.Code != c.want[i] {
				t.Errorf("%s as %s = %d want %d: %s", c.name, g, w.Code, c.want[i], w.Body)
			}
		}
	}
	// PRD-014: a product role sees only that product's stacks, and the namespaces they sit in.
	var l struct{ Items []map[string]any }
	json.Unmarshal(p.do("GET", "/api/v1/namespaces/p/stacks", "", p.sop).Body.Bytes(), &l)
	if len(l.Items) != 1 || l.Items[0]["slug"] != "api" || l.Items[0]["product"] != "shop" || l.Items[0]["application"] != "api" || l.Items[0]["environment"] != "prod" {
		t.Fatalf("product operator sees %v", l.Items)
	}
	json.Unmarshal(p.do("GET", "/api/v1/namespaces", "", p.sop).Body.Bytes(), &l)
	got := []string{}
	for _, n := range l.Items {
		got = append(got, n["slug"].(string))
	}
	if strings.Join(got, ",") != "a,b,c,p" {
		t.Fatalf("namespaces listed for a product operator: %v", got)
	}
	if w := p.do("GET", "/api/v1/namespaces/p", "", p.sop); w.Code != 404 { // a namespace resource needs a namespace or global role
		t.Fatalf("product operator reads namespace = %d", w.Code)
	}
	json.Unmarshal(p.do("GET", "/api/v1/inventory", "", p.sop).Body.Bytes(), &l)
	if len(l.Items) != 4 {
		t.Fatalf("inventory for a product operator = %d stacks", len(l.Items))
	}
	// the product operator deploys in two namespaces with no namespace binding (non-prod: no grant needed)
	for _, ns := range []string{"a", "c"} {
		w := p.do("POST", "/api/v1/namespaces/"+ns+"/stacks/api/deployments", `{"revision":1,"confirm_safety_profile":"as_is","confirm_environment_tier":"non-prod","reason":"go"}`, p.sop)
		if w.Code != 202 {
			t.Fatalf("deploy in %s = %d %s", ns, w.Code, w.Body)
		}
	}
	// ...but not where a namespace refuses the product (PRD-012, POL-R15)
	p.must(201, "POST", "/api/v1/products/shop/applications", `{"slug":"api2","name":"API2"}`, p.sop)
	p.must(200, "PUT", "/api/v1/namespaces/c/policy", `{"allowed_products":["other"]}`, p.admin)
	if w := p.do("POST", "/api/v1/products/shop/applications/api2/stacks", `{"environment":"qa"}`, p.sop); w.Code != 403 || !strings.Contains(w.Body.String(), "product_not_allowed_in_namespace") {
		t.Fatalf("placement into a namespace that excludes the product = %d %s", w.Code, w.Body)
	}
	if w := p.do("POST", "/api/v1/namespaces/c/stacks", `{"slug":"w2","asset":"web","target":"home"}`, p.admin); w.Code != 403 { // unassigned is excluded too
		t.Fatalf("legacy create into restricted namespace = %d", w.Code)
	}
	p.m7 = nil
	p.s.wg.Wait()
	if p.audits("stack.create", "denied") < 2 {
		t.Fatal("refusals not audited")
	}
}

// PRD-044: new stacks of a product go through an environment; legacy create is for `unassigned` only.
func TestPRD044_LegacyStackCreate(t *testing.T) {
	p := newPRD(t)
	p.must(400, "POST", "/api/v1/namespaces/a/stacks", `{"slug":"x","asset":"api","target":"home","product":"shop"}`, p.admin) // environment_required
	p.must(201, "POST", "/api/v1/products/other/applications", `{"slug":"api","name":"API"}`, p.admin)
	if w := p.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"x","asset":"api","target":"home"}`, p.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "asset_ambiguous") {
		t.Fatalf("ambiguous slug = %d %s", w.Code, w.Body)
	}
	p.must(409, "POST", "/api/v1/products/shop/applications/api/stacks", `{"environment":"dev"}`, p.sop) // already placed there
	p.must(400, "POST", "/api/v1/products/shop/applications/api/stacks", `{"environment":"nope"}`, p.sop)
}

// PRD-020, PRD-021, GRT-020, GRT-027: approver resolution table, no self-approval, one product's group cannot
// approve another's, unassigned falls back to the namespace group, a product always has a group.
func TestPRD021_ApproverResolution(t *testing.T) {
	p := newPRD(t)
	p.must(200, "PATCH", "/api/v1/namespaces/b", `{"approver_group":"b-ns-apr"}`, p.admin) // namespace group exists but must not approve shop
	shopB := `{"target":"home","namespace":"b","product":"shop","reason":"release 42","duration_minutes":30}`
	g := p.must(201, "POST", "/api/v1/access-grants", shopB, p.sop)
	if g["product"] != "shop" || g["environment"] != "staging" || g["approver_group"] != "shop-apr" || g["environment_tier"] != "prod" {
		t.Fatalf("grant shape %v", g)
	}
	id := g["id"].(string)
	for who, h := range map[string]map[string]string{
		"other product's group":       p.oapr,
		"namespace group (non-unas.)": p.bnsapr,
		"requester":                   p.sop,
		"break-glass":                 p.admin,
		"product admin, not a member": p.sadm,
	} {
		if w := p.do("POST", "/api/v1/access-grants/"+id+"/approve", "", h); w.Code != 403 && w.Code != 404 {
			t.Errorf("%s approve = %d", who, w.Code)
		}
	}
	// self-approval: a requester who is also in the approver group is still refused
	g2 := p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"p","product":"shop","reason":"release 43","duration_minutes":30}`, p.sopa)
	if w := p.do("POST", "/api/v1/access-grants/"+g2["id"].(string)+"/approve", "", p.sopa); w.Code != 403 || !strings.Contains(w.Body.String(), "self_approval") {
		t.Fatalf("self approval = %d %s", w.Code, w.Body)
	}
	if w := p.do("POST", "/api/v1/access-grants/"+g2["id"].(string)+"/approve", "", p.pnsapr); w.Code != 403 { // prod ns group cannot approve a real product
		t.Fatalf("namespace group approves shop = %d", w.Code)
	}
	if a := p.must(200, "POST", "/api/v1/access-grants/"+id+"/approve", "", p.sapr); a["status"] != "active" || a["approver"] != "usr_sapr" {
		t.Fatalf("product group approve %v", a)
	}
	// unassigned (and a request without product, PRD-022) resolves to the namespace group
	u := p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"p","reason":"legacy fix 1","duration_minutes":30}`, p.pop)
	if u["product"] != "unassigned" || u["approver_group"] != "p-ns-apr" {
		t.Fatalf("unassigned grant %v", u)
	}
	uid := u["id"].(string)
	if w := p.do("POST", "/api/v1/access-grants/"+uid+"/approve", "", p.sapr); w.Code != 403 {
		t.Fatalf("product group approves unassigned = %d", w.Code)
	}
	if a := p.must(200, "POST", "/api/v1/access-grants/"+uid+"/approve", "", p.pnsapr); a["status"] != "active" {
		t.Fatalf("namespace group approves unassigned: %v", a)
	}
	// GRT-027: unassigned in a namespace without a group is impossible; a product without a group cannot exist
	p.s.db.Exec(`UPDATE namespace SET approver_group=NULL WHERE slug='p'`)
	if w := p.do("POST", "/api/v1/access-grants", `{"target":"home","namespace":"p","reason":"legacy fix 2","duration_minutes":30}`, p.admin); w.Code != 409 {
		t.Fatalf("unassigned without any group = %d", w.Code)
	}
	p.must(400, "POST", "/api/v1/products", `{"slug":"nogroup","name":"N"}`, p.admin)
	p.must(400, "PATCH", "/api/v1/products/shop", `{"approver_group":""}`, p.admin)
	p.must(403, "PATCH", "/api/v1/products/shop", `{"approver_group":"x"}`, p.sadm) // PRD-013
	p.must(200, "PATCH", "/api/v1/products/shop", `{"approver_group":"shop-apr2"}`, p.admin)
	if p.audits("product.approver_group_change", "success") != 1 {
		t.Fatal("approver_group_change not audited")
	}
	// the approval check uses the CURRENT resolved group (PRD-022): the old group no longer approves
	g3 := p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"b","product":"shop","reason":"release 44","duration_minutes":30}`, p.both)
	if w := p.do("POST", "/api/v1/access-grants/"+g3["id"].(string)+"/approve", "", p.sapr); w.Code != 403 {
		t.Fatalf("stale group approves = %d", w.Code)
	}
	// PRD-023
	if me := p.must(200, "GET", "/api/v1/me", "", p.sapr); me["approver_for_products"] == nil {
		t.Fatalf("/me %v", me)
	}
	me := jm(p.do("GET", "/api/v1/me", "", p.oapr))
	if l, _ := me["approver_for_products"].([]any); len(l) != 1 || l[0] != "other" {
		t.Fatalf("approver_for_products = %v", me["approver_for_products"])
	}
}

// PRD-020, GRT-017, PRD-043: grant scope is (target, namespace, product); staging is prod; GRT-014 audit carries product.
func TestPRD020_GrantScopeAndTier(t *testing.T) {
	p := newPRD(t)
	dep := func(h map[string]string, ns, stack string) *httptest.ResponseRecorder {
		return p.do("POST", "/api/v1/namespaces/"+ns+"/stacks/"+stack+"/deployments", `{"revision":1,"confirm_safety_profile":"as_is","confirm_environment_tier":"prod","reason":"go"}`, h)
	}
	// staging on a non-prod namespace and target is a prod stack: it needs a grant
	if st := jm(p.do("GET", "/api/v1/namespaces/b/stacks/api", "", p.sop)); st["environment_tier"] != "prod" {
		t.Fatalf("staging tier %v", st)
	}
	w := dep(p.sop, "b", "api")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "grant_required") || !strings.Contains(w.Body.String(), `"product":"shop"`) {
		t.Fatalf("staging without grant = %d %s", w.Code, w.Body)
	}
	// a grant for shop in p...
	g := p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"p","product":"shop","reason":"release 45","duration_minutes":30}`, p.both)
	p.must(200, "POST", "/api/v1/access-grants/"+g["id"].(string)+"/approve", "", p.sapr)
	if w := dep(p.both, "p", "api"); w.Code != 202 {
		t.Fatalf("shop deploy under shop grant = %d %s", w.Code, w.Body)
	}
	// ...does not let the same user deploy other's app in the same namespace
	if w := dep(p.both, "p", "worker"); w.Code != 403 || !strings.Contains(w.Body.String(), "grant_required") {
		t.Fatalf("other product in a shared namespace under shop's grant = %d %s", w.Code, w.Body)
	}
	p.s.wg.Wait()
	if !strings.Contains(auditDetails(p, "deployment.request"), `"product":"shop"`) {
		t.Fatal("grant-gated deployment audit has no product (PRD-024)")
	}
	// a grant request for a scope that is not prod is refused; an environment that is prod makes it prod
	p.must(400, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"a","product":"shop","reason":"release 46","duration_minutes":30}`, p.sop)
	p.must(200, "PATCH", "/api/v1/products/shop/environments/dev", `{"tier":"prod","confirm_prod":"shop"}`, p.sadm)
	p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"a","product":"shop","reason":"release 46","duration_minutes":30}`, p.sop)
	// raising needs the typed confirmation; lowering needs global_admin (PRD-046, D-122)
	p.must(400, "PATCH", "/api/v1/products/shop/environments/qa", `{"tier":"prod"}`, p.sadm)
	p.must(403, "PATCH", "/api/v1/products/shop/environments/dev", `{"tier":"non-prod"}`, p.sadm)
	p.must(200, "PATCH", "/api/v1/products/shop/environments/dev", `{"tier":"non-prod"}`, p.admin)
	p.must(409, "PATCH", "/api/v1/products/shop/environments/dev", `{"namespace":"d"}`, p.admin) // has stacks: PRD-046
	p.must(400, "POST", "/api/v1/products/shop/environments", `{"name":"x","tier":"prod","target":"home","namespace":"d"}`, p.sadm)
	p.must(403, "POST", "/api/v1/products/shop/environments", `{"name":"x","tier":"non-prod","target":"home","namespace":"d"}`, p.sop)
	p.must(409, "POST", "/api/v1/products/shop/environments", `{"name":"dev2","tier":"non-prod","target":"home","namespace":"a"}`, p.sadm) // PRD-040 placement unique
	p.must(409, "DELETE", "/api/v1/products/shop/environments/dev", "", p.sadm)
	p.must(201, "POST", "/api/v1/products/shop/environments", `{"name":"sbx","tier":"non-prod","target":"home","namespace":"d"}`, p.sadm)
	p.must(204, "DELETE", "/api/v1/products/shop/environments/sbx", "", p.sadm)
	// PRD-003/PRD-004: reassignment is global_admin only, audited; the app's grants stop covering it
	p.must(201, "POST", "/api/v1/products/shop/applications", `{"slug":"tmp","name":"T"}`, p.sop)
	p.must(403, "PATCH", "/api/v1/products/shop/applications/tmp", `{"product":"other"}`, p.sadm)
	p.must(200, "PATCH", "/api/v1/products/shop/applications/tmp", `{"product":"other"}`, p.admin)
	if p.audits("application.reassign", "success") != 1 || p.audits("environment.update", "success") < 2 {
		t.Fatal("reassign / environment audit missing")
	}
}

func auditDetails(p *prd, action string) string {
	var d string
	p.s.db.QueryRow(`SELECT group_concat(details) FROM audit_event WHERE action=?`, action).Scan(&d)
	return d
}

// PRD CRUD: product/binding validation and the built-in unassigned product (PRD-001, PRD-006, DOM-015, PRD-010).
func TestPRD001_006_ProductCRUD(t *testing.T) {
	p := newPRD(t)
	for _, c := range []struct {
		body string
		h    map[string]string
		want int
	}{
		{`{"slug":"x1","name":"X","approver_group":"g","tags":["a b"]}`, p.admin, 400},
		{`{"slug":"x1","name":"X","approver_group":"g","links":[{"label":"l","url":"ftp://x"}]}`, p.admin, 400},
		{`{"slug":"x1","name":"X","approver_group":"g"}`, p.sadm, 403},
		{`{"slug":"unassigned","name":"X","approver_group":"g"}`, p.admin, 409},
		{`{"slug":"x1","name":"X","approver_group":"g","links":[{"label":"wiki","url":"https://w.example"}],"tags":["a"]}`, p.admin, 201},
	} {
		if w := p.do("POST", "/api/v1/products", c.body, c.h); w.Code != c.want {
			t.Errorf("POST /products %s = %d want %d: %s", c.body, w.Code, c.want, w.Body)
		}
	}
	var l struct{ Items []map[string]any }
	json.Unmarshal(p.do("GET", "/api/v1/products?tag=retail", "", p.admin).Body.Bytes(), &l)
	if len(l.Items) != 1 || l.Items[0]["slug"] != "shop" {
		t.Fatalf("tag filter %v", l.Items)
	}
	json.Unmarshal(p.do("GET", "/api/v1/products", "", p.sop).Body.Bytes(), &l)
	if len(l.Items) < 1 {
		t.Fatalf("product operator lists %v", l.Items)
	}
	for _, it := range l.Items {
		if it["slug"] == "other" && it["counts"].(map[string]any)["stacks"] != float64(1) { // visible only via stacks in p? sop has no role there
			t.Logf("counts %v", it["counts"])
		}
	}
	if w := p.do("GET", "/api/v1/products", "", p.none); w.Code != 200 || strings.Contains(w.Body.String(), `"slug"`) {
		t.Fatalf("outsider lists %s", w.Body)
	}
	shop := jm(p.do("GET", "/api/v1/products/shop", "", p.sop))
	if c := shop["counts"].(map[string]any); c["applications"] != float64(1) || c["stacks"] != float64(4) || c["prod"] != float64(2) {
		t.Fatalf("counts %v", c)
	}
	p.must(200, "PATCH", "/api/v1/products/shop", `{"owner":"team-shop"}`, p.sadm)
	p.must(409, "DELETE", "/api/v1/products/unassigned", "", p.admin)
	p.must(409, "DELETE", "/api/v1/products/shop", "", p.admin) // has applications (PRD-006)
	p.must(403, "DELETE", "/api/v1/products/x1", "", p.sadm)
	sub, _, _ := p.s.bus.Subscribe(nil)
	p.must(204, "DELETE", "/api/v1/products/x1", "", p.admin)
	p.must(400, "PATCH", "/api/v1/products/unassigned", `{"name":"Renamed"}`, p.admin)
	p.must(400, "PATCH", "/api/v1/products/unassigned", `{"approver_group":"g"}`, p.admin)
	// application routes
	p.must(400, "POST", "/api/v1/products/shop/applications", `{"slug":"bad_slug","name":"X"}`, p.sop)
	p.must(403, "POST", "/api/v1/products/shop/applications", `{"slug":"x","name":"X"}`, p.pop)
	p.must(409, "POST", "/api/v1/products/shop/applications", `{"slug":"api","name":"X"}`, p.sop)
	p.must(201, "POST", "/api/v1/products/shop/applications", `{"slug":"gitops","name":"G","managed_by":"flux","external_ref":"flux/gitops"}`, p.sop)
	p.must(400, "POST", "/api/v1/products/shop/applications", `{"slug":"g2","name":"G","external_ref":"x"}`, p.sop) // control8 apps have no external_ref
	p.must(409, "POST", "/api/v1/products/shop/applications/gitops/stacks", `{"environment":"dev"}`, p.sop)         // tool_managed (PRD-005)
	p.must(409, "DELETE", "/api/v1/products/shop/applications/api", "", p.sadm)                                     // has stacks
	p.must(204, "DELETE", "/api/v1/products/shop/applications/gitops", "", p.sadm)
	p.must(403, "DELETE", "/api/v1/products/shop/applications/api", "", p.sop)
	// role-binding API: product bindings, at most one scope (PRD-010)
	p.must(400, "POST", "/api/v1/role-bindings", `{"idp_group":"g","namespace":"a","product":"shop","role":"viewer"}`, p.admin)
	p.must(400, "POST", "/api/v1/role-bindings", `{"idp_group":"g","product":"nope","role":"viewer"}`, p.admin)
	p.must(400, "POST", "/api/v1/role-bindings", `{"idp_group":"g","product":"shop","role":"global_admin"}`, p.admin)
	rb := p.must(201, "POST", "/api/v1/role-bindings", `{"idp_group":"g","product":"shop","role":"viewer"}`, p.admin)
	if rb["product"] != "shop" || rb["namespace"] != nil {
		t.Fatalf("binding %v", rb)
	}
	if !strings.Contains(p.do("GET", "/api/v1/role-bindings", "", p.admin).Body.String(), `"product":"shop"`) {
		t.Fatal("binding list has no product")
	}
	if _, err := p.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,product_id,role) VALUES('x','g','a','prd_unassigned','viewer')`); err == nil {
		t.Fatal("database accepted a binding with namespace and product")
	}
	for _, a := range []string{"product.create", "product.update", "product.delete", "environment.create", "asset.create", "stack.create", "role_binding.create"} {
		if p.audits(a, "success") == 0 {
			t.Errorf("no success audit for %s", a)
		}
	}
	// R-8: product.removed reached a subscriber that can see products (global)
	found := false
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Type == "product.removed" {
			found = p.s.visible(&auth.Principal{ID: "x", Groups: []string{"unbound"}}, ev) == false
		}
	}
	if !found {
		t.Fatal("product.removed not published (or visible to an outsider)")
	}
}

// 10 events (M17): product role holders get stack/deployment events and product.* events; outsiders do not;
// access_grant.updated goes to requester, resolved approver group, product admins and global_admin (PRD-024).
func TestPRD011_024_Events(t *testing.T) {
	p := newPRD(t)
	pr := func(id string, groups ...string) *auth.Principal {
		return &auth.Principal{ID: id, Groups: groups, AuthMethod: "oidc"}
	}
	sop, sadm, other, oapr, sapr, admin := pr("u1", "sop"), pr("u2", "sadm"), pr("u3", "unbound"), pr("u4", "oth-apr"), pr("u5", "shop-apr"), pr("u6")
	p.bind("ev_ga", "ga", "", "", "global_admin")
	admin = pr("u6", "ga")
	var shopID string
	p.s.db.QueryRow(`SELECT id FROM product WHERE slug='shop'`).Scan(&shopID)
	sub, _, _ := p.s.bus.Subscribe(nil)
	if w := p.do("POST", "/api/v1/namespaces/c/stacks/api/deployments", `{"revision":1,"confirm_safety_profile":"as_is","confirm_environment_tier":"non-prod","reason":"go"}`, p.sop); w.Code != 202 {
		t.Fatalf("deploy %d %s", w.Code, w.Body)
	}
	p.s.wg.Wait()
	p.must(200, "PATCH", "/api/v1/products/shop", `{"description":"x"}`, p.sadm)
	p.must(201, "POST", "/api/v1/access-grants", `{"target":"home","namespace":"p","product":"shop","reason":"release 47","duration_minutes":30}`, p.sop)
	seen := map[string]int{}
	for len(sub.C) > 0 {
		ev := <-sub.C
		switch ev.Type {
		case "deployment.updated", "stack.updated":
			if ev.Product != shopID || !p.s.visible(sop, ev) || p.s.visible(other, ev) || !p.s.visible(sadm, ev) {
				t.Errorf("%s: product=%q sop=%v other=%v", ev.Type, ev.Product, p.s.visible(sop, ev), p.s.visible(other, ev))
			}
			seen[ev.Type]++
		case "product.updated", "application.updated", "environment.updated":
			if !p.s.visible(sop, ev) || p.s.visible(other, ev) {
				t.Errorf("%s visibility", ev.Type)
			}
			seen[ev.Type]++
		case "access_grant.updated":
			if !p.s.visible(sapr, ev) || !p.s.visible(sadm, ev) || !p.s.visible(admin, ev) || p.s.visible(oapr, ev) || p.s.visible(other, ev) {
				t.Errorf("grant event audience sapr=%v sadm=%v oapr=%v", p.s.visible(sapr, ev), p.s.visible(sadm, ev), p.s.visible(oapr, ev))
			}
			seen[ev.Type]++
		}
	}
	for _, typ := range []string{"deployment.updated", "stack.updated", "product.updated", "access_grant.updated"} {
		if seen[typ] == 0 {
			t.Errorf("no %s published", typ)
		}
	}
}

// PRD-030, PRD-031, PRD-011: the matrix lists the product's environments and applications, only what the caller sees.
func TestPRD030_Overview(t *testing.T) {
	p := newPRD(t)
	p.must(202, "POST", "/api/v1/namespaces/a/stacks/api/deployments", `{"revision":1,"confirm_safety_profile":"as_is","confirm_environment_tier":"non-prod","reason":"go"}`, p.sop)
	p.s.wg.Wait()
	ov := p.must(200, "GET", "/api/v1/products/shop/overview", "", p.sop)
	envs := ov["environments"].([]any)
	if len(envs) != 4 || envs[0].(map[string]any)["name"] != "dev" || envs[2].(map[string]any)["tier"] != "prod" {
		t.Fatalf("environments %v", envs)
	}
	apps := ov["applications"].([]any)
	cells := apps[0].(map[string]any)["cells"].([]any)
	if len(apps) != 1 || len(cells) != 4 {
		t.Fatalf("apps %v", apps)
	}
	var dev map[string]any
	for _, c := range cells {
		if c.(map[string]any)["environment"] == "dev" {
			dev = c.(map[string]any)
		}
	}
	if dev["state"] != "active" || dev["desired_revision"] != float64(1) || dev["namespace"] != "a" || dev["target"] != "home" || dev["images"] == nil {
		t.Fatalf("dev cell %v", dev)
	}
	if tot := ov["totals"].(map[string]any); tot["stacks"] != float64(4) || tot["prod"] != float64(2) || tot["apps"] != float64(1) {
		t.Fatalf("totals %v", tot)
	}
	p.bind("ov_nv", "nv", "a", "", "viewer")
	ov = p.must(200, "GET", "/api/v1/products/shop/overview", "", p.user("nv", `["nv"]`)) // namespace viewer: only their cell
	if cells := ov["applications"].([]any)[0].(map[string]any)["cells"].([]any); len(cells) != 1 {
		t.Fatalf("namespace viewer sees %d cells", len(cells))
	}
	p.must(404, "GET", "/api/v1/products/shop/overview", "", p.none)
	// legacy stacks form a column without environment name
	un := p.must(200, "GET", "/api/v1/products/unassigned/overview", "", p.admin)
	legacy := un["environments"].([]any)
	if len(legacy) != 1 || legacy[0].(map[string]any)["name"] != nil || legacy[0].(map[string]any)["namespace"] != "p" {
		t.Fatalf("legacy column %v", legacy)
	}
	if c := un["applications"].([]any)[0].(map[string]any)["cells"].([]any)[0].(map[string]any); c["environment"] != nil || c["stack"] != "web" {
		t.Fatalf("legacy cell %v", c)
	}
}

// 12 API table: asset list filter by product, deprecated POST product param, application slug resolution.
func TestPRD_AssetsFilterAndLegacy(t *testing.T) {
	p := newPRD(t)
	names := func(path string) string {
		var l struct{ Items []map[string]any }
		json.Unmarshal(p.do("GET", path, "", p.admin).Body.Bytes(), &l)
		var s []string
		for _, it := range l.Items {
			s = append(s, it["product"].(string)+"/"+it["slug"].(string))
		}
		return strings.Join(s, ",")
	}
	if got := names("/api/v1/namespaces/p/assets"); got != "shop/api,unassigned/web,other/worker" {
		t.Fatalf("all in p: %s", got)
	}
	if got := names("/api/v1/namespaces/p/assets?product=other"); got != "other/worker" {
		t.Fatalf("filter other: %s", got)
	}
	if got := names("/api/v1/namespaces/p/assets?product=unassigned"); got != "unassigned/web" {
		t.Fatalf("filter unassigned: %s", got)
	}
	a := p.must(201, "POST", "/api/v1/namespaces/a/assets", `{"slug":"legacy","name":"L"}`, p.admin) // default product
	if a["product"] != "unassigned" || a["namespace"] != "a" {
		t.Fatalf("legacy create %v", a)
	}
	a = p.must(201, "POST", "/api/v1/namespaces/a/assets", `{"slug":"viaprod","name":"L","product":"shop"}`, p.admin)
	if a["product"] != "shop" || a["namespace"] != nil {
		t.Fatalf("legacy create with product %v", a)
	}
	p.must(404, "POST", "/api/v1/namespaces/a/assets", `{"slug":"nope","name":"L","product":"other"}`, p.pop) // API-005: pop cannot see other at all
}
