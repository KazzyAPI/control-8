package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/events"
)

const m6Deploy = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: 1
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      containers:
        - {name: web, image: "ghcr.io/acme/web:%s"%s}
%s`

func sprintfM6(tag, ctr, extra string) string {
	r := strings.Replace(m6Deploy, "%s", tag, 1)
	r = strings.Replace(r, "%s", ctr, 1)
	return strings.Replace(r, "%s", extra, 1)
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func m6env(t *testing.T) (e *env, admin, op, viewer, other map[string]string) {
	e = newEnv(t)
	admin, _ = e.login()
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','va','a','viewer'),('r2','oa','a','operator'),('r3','aa','a','admin')`)
	e.s.db.Exec(`INSERT INTO target(name,id,kind,environment_tier) VALUES('home','tgt_1','kubernetes','non-prod')`)
	for _, ns := range []string{"a", "b"} {
		e.do("POST", "/api/v1/namespaces", `{"slug":"`+ns+`","display_name":"x","environment_tier":"non-prod"}`, admin)
	}
	e.do("POST", "/api/v1/namespaces/a/assets", `{"slug":"web","name":"Web"}`, admin)
	e.do("POST", "/api/v1/namespaces/a/assets", `{"slug":"gitops","name":"G","managed_by":"argo"}`, admin)
	return e, admin, e.user("oa", `["oa"]`), e.user("va", `["va"]`), e.user("zz", `[]`)
}

// K8S-004, API-005, DOM-009, DOM-005, R-8: stack create/delete, events, asset delete with stacks.
func TestK8S004_StackLifecycle(t *testing.T) {
	e, admin, op, viewer, other := m6env(t)
	sub, _, _ := e.s.bus.Subscribe(nil)
	mk := func(h map[string]string, body string) int {
		return e.do("POST", "/api/v1/namespaces/a/stacks", body, h).Code
	}
	good := `{"slug":"s1","asset":"web","target":"home"}`
	for _, c := range []struct {
		h    map[string]string
		body string
		want int
	}{
		{viewer, good, 403}, {other, good, 404},
		{op, `{"slug":"s1","asset":"web","target":"home","runtime":"docker"}`, 400}, // runtime_target_mismatch
		{op, `{"slug":"s1","asset":"gitops","target":"home"}`, 409},                 // argo_managed
		{op, `{"slug":"s1","asset":"nope","target":"home"}`, 400},
		{op, `{"slug":"s1","asset":"web","target":"nope"}`, 400},
		{op, good, 201}, {op, good, 409},
	} {
		if got := mk(c.h, c.body); got != c.want {
			t.Errorf("create %s as %v = %d want %d", c.body, c.h["Cookie"][:12], got, c.want)
		}
	}
	if w := e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s2","asset":"web","target":"home","runtime":"docker"}`, op); !strings.Contains(w.Body.String(), "runtime_target_mismatch") {
		t.Fatal(w.Body)
	}
	if w := e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s2","asset":"gitops","target":"home"}`, op); !strings.Contains(w.Body.String(), "argo_managed") {
		t.Fatal(w.Body)
	}
	var st map[string]any
	w := e.do("GET", "/api/v1/namespaces/a/stacks/s1", "", viewer)
	json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st["state"] != "draft" || st["safety_profile"] != nil || st["runtime"] != "kubernetes" ||
		st["environment_tier"] != "non-prod" || st["k8s_namespace"] != "a" || st["target"] != "home" {
		t.Fatalf("%d %v", w.Code, st)
	}
	if w := e.do("GET", "/api/v1/namespaces/b/stacks/s1", "", viewer); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := e.do("GET", "/api/v1/namespaces/a/stacks", "", viewer); !strings.Contains(w.Body.String(), `"s1"`) {
		t.Fatal(w.Body)
	}
	// stack.updated reaches a viewer of a, not an outsider
	var got *events.Event
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Type == "stack.updated" {
			got = &ev
		}
	}
	if got == nil || !e.s.visible(&auth.Principal{ID: "u", Groups: []string{"va"}}, *got) || e.s.visible(&auth.Principal{ID: "u2"}, *got) {
		t.Fatalf("stack.updated %+v", got)
	}
	// asset with stacks cannot be deleted
	if w := e.do("DELETE", "/api/v1/namespaces/a/assets/web", "", admin); w.Code != 409 {
		t.Fatalf("asset delete with stack = %d", w.Code)
	}
	// draft delete: operator ok, viewer 403; stack.removed published
	if w := e.do("DELETE", "/api/v1/namespaces/a/stacks/s1", "", viewer); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := e.do("DELETE", "/api/v1/namespaces/a/stacks/s1", "", op); w.Code != 204 {
		t.Fatal(w.Code)
	}
	var removed bool
	for len(sub.C) > 0 {
		removed = removed || (<-sub.C).Type == "stack.removed"
	}
	if !removed {
		t.Fatal("no stack.removed")
	}
	for _, a := range []string{"stack.create", "stack.delete"} {
		if e.audits(a, "success") != 1 {
			t.Errorf("audit %s", a)
		}
	}
	if e.audits("stack.create", "denied") != 2 || e.audits("stack.create", "failed") != 3 {
		t.Errorf("stack.create denied/failed audits: %d/%d", e.audits("stack.create", "denied"), e.audits("stack.create", "failed"))
	}
}

// K8S-020..026, POL-R12..R14, R7, R4, API-003 revision rules, DOM-002, revision.created event, diff.
func TestK8S022_RevisionRenderAndPolicy(t *testing.T) {
	e, _, op, viewer, other := m6env(t)
	e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s1","asset":"web","target":"home"}`, op)
	sub, _, _ := e.s.bus.Subscribe(nil)
	path := "/api/v1/namespaces/a/stacks/s1/revisions"
	body := func(profile, manifests string) string {
		return `{"safety_profile":` + jsonStr(profile) + `,"manifests":` + jsonStr(manifests) + `}`
	}
	type rev struct {
		Number   int
		Profile  string `json:"safety_profile"`
		Rendered []map[string]any
		Policy   struct {
			Allowed          bool
			RequiresApproval bool `json:"requires_approval"`
			Violations       []struct{ Rule string }
		} `json:"policy_result"`
	}
	post := func(h map[string]string, b string) (int, rev, string) {
		w := e.do("POST", path, b, h)
		var r rev
		json.Unmarshal(w.Body.Bytes(), &r)
		return w.Code, r, w.Body.String()
	}
	c, r1, raw := post(op, body("managed_rollout", sprintfM6("1", "", "")))
	if c != 201 || r1.Number != 1 || !r1.Policy.Allowed || len(r1.Rendered) != 1 {
		t.Fatalf("%d %s", c, raw)
	}
	md := r1.Rendered[0]["metadata"].(map[string]any)["labels"].(map[string]any)
	tpl := r1.Rendered[0]["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
	sel := r1.Rendered[0]["spec"].(map[string]any)["selector"].(map[string]any)["matchLabels"].(map[string]any)
	if md["control-8/revision"] != "1" || md["control-8/stack"] == "" || md["control-8/safety-profile"] != "managed_rollout" ||
		tpl["control-8/managed"] != "true" || tpl["control-8/revision"] != nil || len(sel) != 1 {
		t.Fatalf("labels %v %v %v", md, tpl, sel)
	}
	// first apply of a database workload needs approval (POL-R9)
	_, rdb, _ := post(op, body("as_is", strings.Replace(sprintfM6("1", "", ""), "ghcr.io/acme/web", "postgres", 1)))
	if !rdb.Policy.RequiresApproval {
		t.Fatalf("db approval: %+v", rdb.Policy)
	}

	bad := map[string]struct{ profile, manifests, rule string }{
		"secret":      {"as_is", "apiVersion: v1\nkind: Secret\nmetadata: {name: s}\n", "kind_not_allowed"},
		"clusterrole": {"as_is", "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata: {name: s}\n", "kind_not_allowed"},
		"namespace":   {"as_is", "apiVersion: v1\nkind: Namespace\nmetadata: {name: s}\n", "kind_not_allowed"},
		"hostPath":    {"as_is", sprintfM6("1", "", "") + "", "host_path_not_allowed"},
		"privileged":  {"as_is", sprintfM6("1", `, securityContext: {privileged: true}`, ""), "privileged_not_allowed"},
		"other ns":    {"as_is", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: s, namespace: kube-system}\n", "namespace_not_allowed"},
		"label":       {"as_is", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: s, labels: {control-8/x: y}}\n", "label_forbidden"},
	}
	b := bad["hostPath"]
	b.manifests = strings.Replace(sprintfM6("1", "", ""), "      containers:", "      volumes: [{name: v, hostPath: {path: /etc}}]\n      containers:", 1)
	bad["hostPath"] = b
	for name, x := range bad {
		c, r, raw := post(op, body(x.profile, x.manifests))
		if c != 201 || r.Policy.Allowed || len(r.Policy.Violations) == 0 || r.Policy.Violations[0].Rule != x.rule {
			t.Errorf("%s: %d %s", name, c, raw)
		}
	}
	for name, x := range map[string]struct {
		body, code string
		status     int
	}{
		"parachute":   {body("parachute", sprintfM6("1", "", "")), "safety_profile_invalid_for_runtime", 400},
		"compose":     {`{"safety_profile":"as_is","compose":"services: {}"}`, "spec_runtime_mismatch", 400},
		"no manifest": {`{"safety_profile":"as_is"}`, "spec_runtime_mismatch", 400},
		"no profile":  {`{"manifests":"x: y"}`, "safety_profile_required", 400},
		"bad yaml":    {body("as_is", "a: ["), "invalid_manifests", 422},
		"list":        {body("as_is", "apiVersion: v1\nkind: List\nmetadata: {name: a}\n"), "invalid_manifests", 422},
		"timeout":     {`{"safety_profile":"as_is","manifests":"a: b","rollout_timeout_seconds":5}`, "invalid_body", 400},
		"poll as_is":  {`{"safety_profile":"as_is","manifests":"a: b","digest_poll":true}`, "invalid_body", 400},
	} {
		if c, _, raw := post(op, x.body); c != x.status || !strings.Contains(raw, x.code) {
			t.Errorf("%s: %d %s", name, c, raw)
		}
	}
	if c, _, _ := post(viewer, body("as_is", "a: b")); c != 403 {
		t.Fatal(c)
	}
	if c, _, _ := post(other, body("as_is", "a: b")); c != 404 {
		t.Fatal(c)
	}
	// numbering, list, get, diff
	var n int
	e.s.db.QueryRow(`SELECT max(number) FROM revision`).Scan(&n)
	if n != 2+len(bad) {
		t.Fatalf("max number %d", n)
	}
	if w := e.do("GET", path, "", viewer); w.Code != 200 || strings.Contains(w.Body.String(), `"rendered"`) {
		t.Fatalf("list %d", w.Code)
	}
	if w := e.do("GET", path+"/1", "", viewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"rendered"`) {
		t.Fatal(w.Code)
	}
	if w := e.do("GET", path+"/99", "", viewer); w.Code != 404 {
		t.Fatal(w.Code)
	}
	post(op, body("managed_rollout", sprintfM6("2", "", "")))
	w := e.do("GET", path+"/"+jsonNum(n+1)+"/diff?against=1", "", viewer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "-        - image") && !strings.Contains(w.Body.String(), "-      - image: ghcr.io/acme/web:1") ||
		!strings.Contains(w.Body.String(), "+      - image: ghcr.io/acme/web:2") {
		t.Fatalf("diff %d\n%s", w.Code, w.Body)
	}
	if w := e.do("GET", path+"/1/diff", "", viewer); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// revision.created reaches the viewer, not an outsider, and carries no manifests
	var seen bool
	for len(sub.C) > 0 {
		ev := <-sub.C
		if ev.Type == "revision.created" {
			b, _ := json.Marshal(ev.Data)
			seen = e.s.visible(&auth.Principal{ID: "u", Groups: []string{"va"}}, ev) && !e.s.visible(&auth.Principal{ID: "u2"}, ev) && !strings.Contains(string(b), `"rendered"`)
		}
	}
	if !seen || e.audits("revision.create", "success") == 0 {
		t.Fatal("revision.created event/audit")
	}
}

func jsonNum(n int) string { b, _ := json.Marshal(n); return string(b) }

// 05 policy endpoints: roles, merge, audit old/new, namespace policy cannot widen global.
func TestPOL_PolicyEndpoints(t *testing.T) {
	e, admin, op, viewer, other := m6env(t)
	aa := e.user("aa", `["aa"]`)
	e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s1","asset":"web","target":"home"}`, op)
	if w := e.do("GET", "/api/v1/policies/global", "", viewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"protected_db_approval":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	g := `{"allowed_registries":["ghcr.io/"],"allowed_k8s_namespaces":[],"required_labels":[],"protected_db_approval":true,"allowed_safety_profiles":["managed_rollout","as_is"],"allowed_bind_prefixes":[],"allow_privileged":false}`
	if w := e.do("PUT", "/api/v1/policies/global", g, aa); w.Code != 403 {
		t.Fatalf("ns admin put global = %d", w.Code)
	}
	if w := e.do("PUT", "/api/v1/policies/global", g, admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/v1/policies/global", `{"allowed_safety_profiles":["x"]}`, admin); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := e.do("PUT", "/api/v1/policies/global", `{"bogus":1}`, admin); w.Code != 400 {
		t.Fatal(w.Code)
	}
	// namespace policy: admin only, viewer reads, cannot widen
	np := "/api/v1/namespaces/a/policy"
	if w := e.do("PUT", np, `{"allowed_registries":["ghcr.io/acme/"]}`, op); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := e.do("PUT", np, `{"allowed_registries":["evil.io/"]}`, aa); w.Code != 400 {
		t.Fatalf("widening = %d", w.Code)
	}
	if w := e.do("PUT", np, `{"allowed_registries":["ghcr.io/acme/"],"required_labels":["team"]}`, aa); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", np, "", viewer); w.Code != 200 || !strings.Contains(w.Body.String(), "ghcr.io/acme/") {
		t.Fatal(w.Body)
	}
	if w := e.do("GET", np, "", other); w.Code != 404 {
		t.Fatal(w.Code)
	}
	var eff struct {
		Registries []string `json:"allowed_registries"`
		Labels     []string `json:"required_labels"`
		Profiles   []string `json:"allowed_safety_profiles"`
	}
	json.Unmarshal(e.do("GET", np+"/effective", "", viewer).Body.Bytes(), &eff)
	if len(eff.Registries) != 1 || eff.Registries[0] != "ghcr.io/acme/" || len(eff.Labels) != 1 || len(eff.Profiles) != 2 {
		t.Fatalf("%+v", eff)
	}
	// effective rules apply at revision create
	w := e.do("POST", "/api/v1/namespaces/a/stacks/s1/revisions", `{"safety_profile":"as_is","manifests":`+jsonStr(sprintfM6("1", "", "")+"")+`}`, op)
	if !strings.Contains(w.Body.String(), "label_missing") {
		t.Fatalf("required label not enforced: %s", w.Body)
	}
	if e.audits("policy.update", "success") != 2 || e.audits("policy.update", "denied") != 2 {
		t.Fatalf("audits %d/%d", e.audits("policy.update", "success"), e.audits("policy.update", "denied"))
	}
	var det string
	e.s.db.QueryRow(`SELECT details FROM audit_event WHERE action='policy.update' AND outcome='success' ORDER BY id LIMIT 1`).Scan(&det)
	if !strings.Contains(det, `"old"`) || !strings.Contains(det, `"new"`) {
		t.Fatal(det)
	}
}
