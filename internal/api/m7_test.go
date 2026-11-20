package api

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"context"

	"github.com/KazzyAPI/control-8/internal/auth"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	"github.com/KazzyAPI/control-8/internal/targets"
	"sigs.k8s.io/yaml"
)

type fakeDriver struct {
	mu                        sync.Mutex
	calls                     []string
	precheck, apply, teardown error
	gate                      chan struct{}       // Apply blocks until closed
	hook                      func(rt.Plan) error // M11: scripted Apply outcome, overrides apply
}

func (f *fakeDriver) rec(c string)                            { f.mu.Lock(); f.calls = append(f.calls, c); f.mu.Unlock() }
func (f *fakeDriver) Precheck(context.Context, rt.Plan) error { f.rec("precheck"); return f.precheck }
func (f *fakeDriver) Apply(_ context.Context, p rt.Plan) (string, error) {
	f.rec("apply")
	if f.gate != nil {
		<-f.gate
	}
	if f.hook != nil {
		return "applied\n", f.hook(p)
	}
	return "applied\n", f.apply
}
func (f *fakeDriver) Prune(context.Context, rt.Plan) (string, error) { f.rec("prune"); return "", nil }
func (f *fakeDriver) Teardown(context.Context, rt.Stack) (string, error) {
	f.rec("teardown")
	return "kept PersistentVolumeClaim/data\n", f.teardown
}

func (f *fakeDriver) Watch(context.Context, rt.WatchOptions) (rt.Cache, error) {
	return nil, errors.New("no watch")
}

const (
	cm  = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cfg}\ndata: {a: b}\n"
	pg  = cm + "---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: db}\nspec:\n  selector: {matchLabels: {app: db}}\n  template:\n    metadata: {labels: {app: db}}\n    spec: {containers: [{name: db, image: ghcr.io/acme/postgres:16}]}\n"
	dep = `{"revision":%d,"confirm_safety_profile":"as_is","confirm_environment_tier":"non-prod","reason":"go"}`
)

type m7 struct {
	*env
	op, aa, viewer, other, admin map[string]string
	drv                          *fakeDriver
}

// m7env: namespace a (non-prod) with stack s1 (draft) on target home, a fake driver, operator/admin/viewer users.
func newM7(t *testing.T) *m7 {
	e, admin, op, viewer, other := m6env(t)
	m := &m7{env: e, op: op, viewer: viewer, other: other, admin: admin, aa: e.user("aa", `["aa"]`), drv: &fakeDriver{}}
	e.s.drivers = func(string) (rt.Driver, error) { return m.drv, nil }
	if w := e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s1","asset":"web","target":"home"}`, op); w.Code != 201 {
		t.Fatal(w.Body)
	}
	return m
}

func (m *m7) revision(manifests string) {
	b, _ := json.Marshal(map[string]any{"safety_profile": "as_is", "manifests": manifests})
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/revisions", string(b), m.op); w.Code != 201 {
		m.t.Fatal(w.Body)
	}
}

func (m *m7) deploy(h map[string]string, n int) (int, map[string]any) {
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", strings.Replace(dep, "%d", string(rune('0'+n)), 1), h)
	var d map[string]any
	json.Unmarshal(w.Body.Bytes(), &d)
	return w.Code, d
}

func (m *m7) get(id string) map[string]any {
	var d map[string]any
	json.Unmarshal(m.do("GET", "/api/v1/deployments/"+id, "", m.viewer).Body.Bytes(), &d)
	return d
}

func (m *m7) stack() map[string]any {
	var d map[string]any
	json.Unmarshal(m.do("GET", "/api/v1/namespaces/a/stacks/s1", "", m.viewer).Body.Bytes(), &d)
	return d
}

// K8S-041, K8S-040, DOM-001, DOM-009, R-8: apply flow, audit, deployment.updated queued -> running -> succeeded, per-viewer filter.
func TestM7_ApplySucceeds(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	sub, _, _ := m.s.bus.Subscribe(nil)
	code, d := m.deploy(m.op, 1)
	if code != 202 || d["status"] != "queued" || d["action"] != "apply" || d["safety_profile"] != "as_is" || d["environment_tier"] != "non-prod" {
		t.Fatalf("%d %v", code, d)
	}
	m.s.wg.Wait()
	got := m.get(d["id"].(string))
	if got["status"] != "succeeded" || got["output"] == "" || got["error"] != nil || got["started_at"] == nil || got["finished_at"] == nil {
		t.Fatalf("%v", got)
	}
	if strings.Join(m.drv.calls, ",") != "precheck,apply,prune" { // 08: precheck, apply, prune (the health wait needs the inventory engine, M10)
		t.Fatalf("calls %v", m.drv.calls)
	}
	if st := m.stack(); st["state"] != "active" || st["desired_revision"] != float64(1) || st["safety_profile"] != "as_is" {
		t.Fatalf("%v", st)
	}
	var seq []string
	for len(sub.C) > 0 {
		ev := <-sub.C
		if ev.Type == "deployment.updated" {
			data := ev.Data.(map[string]any)
			if _, has := data["output"]; has {
				t.Fatal("event carries output (AUTH-043)")
			}
			seq = append(seq, data["status"].(string))
			if !m.s.visible(&auth.Principal{ID: "u", Groups: []string{"va"}}, ev) || m.s.visible(&auth.Principal{ID: "u2"}, ev) {
				t.Fatal("deployment.updated filter")
			}
		}
	}
	if strings.Join(seq, ",") != "queued,running,succeeded" {
		t.Fatalf("events %v", seq)
	}
	if m.audits("deployment.request", "success") != 1 || m.audits("deployment.run", "success") != 1 {
		t.Fatal("audit")
	}
	if w := m.do("GET", "/api/v1/namespaces/a/stacks/s1/deployments", "", m.viewer); !strings.Contains(w.Body.String(), d["id"].(string)) || w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := m.do("GET", "/api/v1/deployments/"+d["id"].(string), "", m.other); w.Code != 404 {
		t.Fatalf("outsider get = %d", w.Code)
	}
}

// K8S-041, K8S-043, DOM-003: a refused precheck writes nothing and leaves the stack draft; an apply failure marks it failed.
func TestM7_PrecheckAndApplyFailures(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	m.drv.precheck = &rt.Error{Code: "ownership_conflict", Detail: "Deployment/web exists and is not owned by this stack"}
	_, d := m.deploy(m.op, 1)
	m.s.wg.Wait()
	got := m.get(d["id"].(string))
	if got["status"] != "failed" || !strings.HasPrefix(got["error"].(string), "ownership_conflict:") || strings.Contains(strings.Join(m.drv.calls, ","), "apply") {
		t.Fatalf("%v %v", got, m.drv.calls)
	}
	if m.stack()["state"] != "draft" {
		t.Fatal("precheck failure changed the stack")
	}
	m.drv.precheck, m.drv.apply = nil, &rt.Error{Code: "ownership_conflict", Detail: `conflict with "kubectl-edit"`}
	_, d = m.deploy(m.op, 1)
	m.s.wg.Wait()
	if m.get(d["id"].(string))["status"] != "failed" || m.stack()["state"] != "failed" {
		t.Fatalf("%v", m.stack())
	}
	if m.audits("deployment.run", "failed") != 2 {
		t.Fatal("audit")
	}
}

// DOM-001: one queued/running deployment per stack.
func TestDOM001_OneOpenDeployment(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	m.drv.gate = make(chan struct{})
	code, _ := m.deploy(m.op, 1)
	if code2, _ := m.deploy(m.op, 1); code != 202 || code2 != 409 {
		t.Fatalf("%d %d", code, code2)
	}
	close(m.drv.gate)
	m.s.wg.Wait()
	if code, _ := m.deploy(m.op, 1); code != 202 {
		t.Fatalf("after finish: %d", code)
	}
	m.s.wg.Wait()
}

// POL-R9, DOM-004, API-005: database first apply needs a second admin; viewers cannot approve; reject and cancel.
func TestPOLR9_ApprovalFlow(t *testing.T) {
	m := newM7(t)
	m.revision(pg)
	if code, _ := m.deploy(m.viewer, 1); code != 403 {
		t.Fatalf("viewer deploy %d", code)
	}
	_, d := m.deploy(m.op, 1)
	id := d["id"].(string)
	if d["status"] != "pending_approval" {
		t.Fatalf("%v", d)
	}
	post := func(h map[string]string, verb, body string) int {
		return m.do("POST", "/api/v1/deployments/"+id+"/"+verb, body, h).Code
	}
	if c := post(m.viewer, "approve", ""); c != 403 {
		t.Fatalf("viewer approve %d", c)
	}
	if c := post(m.other, "approve", ""); c != 404 {
		t.Fatalf("outsider approve %d", c)
	}
	// a namespace admin who is also the requester cannot approve their own request
	_, own := m.deploy(m.aa, 1)
	if own["status"] != "pending_approval" {
		t.Fatalf("%v", own)
	}
	if c := m.do("POST", "/api/v1/deployments/"+own["id"].(string)+"/approve", "", m.aa).Code; c != 403 {
		t.Fatalf("self approve %d", c)
	}
	if m.audits("deployment.approve", "denied") < 2 {
		t.Fatal("denials not audited")
	}
	if c := post(m.admin, "reject", `{}`); c != 400 {
		t.Fatalf("reject without reason %d", c)
	}
	if c := m.do("POST", "/api/v1/deployments/"+own["id"].(string)+"/reject", `{"reason":"no"}`, m.admin).Code; c != 200 {
		t.Fatalf("reject %d", c)
	}
	if c := post(m.admin, "approve", ""); c != 200 {
		t.Fatalf("approve %d", c)
	}
	m.s.wg.Wait()
	got := m.get(id)
	if got["status"] != "succeeded" || got["approved_by"] == nil || got["approved_by"] == got["requested_by"] {
		t.Fatalf("%v", got)
	}
	if c := post(m.admin, "approve", ""); c != 409 {
		t.Fatalf("re-approve %d", c)
	}
	// cancel: requester or admin, only before running
	m.revision(strings.Replace(pg, "postgres:16", "postgres:17", 1)) // changed database spec: approval again
	_, d2 := m.deploy(m.op, 2)
	id2 := d2["id"].(string)
	if c := m.do("POST", "/api/v1/deployments/"+id2+"/cancel", "", m.viewer).Code; c != 403 {
		t.Fatalf("viewer cancel %d", c)
	}
	if c := m.do("POST", "/api/v1/deployments/"+id2+"/cancel", "", m.op).Code; c != 200 || m.get(id2)["status"] != "cancelled" {
		t.Fatalf("cancel %d", c)
	}
	if c := m.do("POST", "/api/v1/deployments/"+id2+"/cancel", "", m.op).Code; c != 409 {
		t.Fatalf("cancel twice %d", c)
	}
	for _, a := range []string{"deployment.approve", "deployment.reject", "deployment.cancel"} {
		if m.audits(a, "success") == 0 {
			t.Errorf("no audit %s", a)
		}
	}
}

// 08 Teardown, POL-R10, D-37: DELETE on a deployed stack requests a teardown; success removes the stack.
func TestM7_Teardown(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	m.deploy(m.op, 1)
	m.s.wg.Wait()
	del := func(body string, h map[string]string) *json.RawMessage {
		w := m.do("DELETE", "/api/v1/namespaces/a/stacks/s1", body, h)
		raw := json.RawMessage(w.Body.Bytes())
		if w.Code != 202 {
			m.t.Logf("delete %d %s", w.Code, w.Body)
			return nil
		}
		return &raw
	}
	if del("", m.op) != nil || del(`{"reason":"bye"}`, m.viewer) != nil || del(`{"reason":"bye","confirm_environment_tier":"prod"}`, m.op) != nil {
		t.Fatal("teardown accepted without reason, as viewer or with wrong tier")
	}
	sub, _, _ := m.s.bus.Subscribe(nil)
	raw := del(`{"reason":"bye"}`, m.op)
	if raw == nil {
		t.Fatal("teardown refused")
	}
	var d map[string]any
	json.Unmarshal(*raw, &d)
	m.s.wg.Wait()
	got := m.get(d["id"].(string))
	if d["action"] != "teardown" || got["status"] != "succeeded" || !strings.Contains(got["output"].(string), "kept PersistentVolumeClaim") || m.stack()["state"] != "removed" {
		t.Fatalf("%v %v", got, m.stack())
	}
	var removed bool
	for len(sub.C) > 0 {
		removed = removed || (<-sub.C).Type == "stack.removed"
	}
	if !removed {
		t.Fatal("no stack.removed")
	}
	if w := m.do("DELETE", "/api/v1/namespaces/a/stacks/s1", `{"reason":"again"}`, m.op); w.Code != 409 {
		t.Fatalf("removed stack: %d", w.Code)
	}
	// POL-R10: database stack -> pending_approval
	m2 := newM7(t)
	m2.revision(pg)
	m2.deploy(m2.op, 1)
	m2.s.wg.Wait()
	_, ids := m2.deploy(m2.op, 1)
	m2.do("POST", "/api/v1/deployments/"+ids["id"].(string)+"/approve", "", m2.admin)
	m2.s.wg.Wait()
	w := m2.do("DELETE", "/api/v1/namespaces/a/stacks/s1", `{"reason":"bye"}`, m2.op)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"pending_approval"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// D-22/K8S-044, GRT (M8 pending, D-71), API-003: refusals before anything is queued.
func TestM7_Refusals(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	m.revision("apiVersion: v1\nkind: Secret\nmetadata: {name: s}\n") // revision 2: policy-violating, DOM-003
	body := func(rev, prof, tier string) string {
		return `{"revision":` + rev + `,"confirm_safety_profile":"` + prof + `","confirm_environment_tier":"` + tier + `"}`
	}
	cases := []struct {
		body string
		want int
	}{
		{body("1", "managed_rollout", "non-prod"), 400}, {body("1", "as_is", "prod"), 400}, {body("9", "as_is", "non-prod"), 404},
		{body("2", "as_is", "non-prod"), 422}, {`{"revision":1,"nope":1}`, 400},
	}
	for _, c := range cases {
		if got := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", c.body, m.op).Code; got != c.want {
			t.Errorf("%s = %d want %d", c.body, got, c.want)
		}
	}
	// prod stack: refused without a grant (GRT-011), with scope, nothing queued
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", body("1", "as_is", "prod"), m.op)
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"grant_required"`) || !strings.Contains(w.Body.String(), `"scope"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m.s.db.Exec(`UPDATE target SET environment_tier='non-prod'`)
	// Argo-managed asset
	m.s.db.Exec(`UPDATE asset SET managed_by='argo'`)
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", body("1", "as_is", "non-prod"), m.op); w.Code != 409 || !strings.Contains(w.Body.String(), "argo_managed") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m.s.db.Exec(`UPDATE asset SET managed_by='control8'`)
	// unreachable target -> 503, nothing queued
	m.s.targets = targets.NewChecker([]targets.Spec{{Name: "home", Kind: "kubernetes"}}, func(targets.Spec) (targets.Versioner, error) { return nil, errors.New("x") })
	m.s.targets.Check("home")
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", body("1", "as_is", "non-prod"), m.op); w.Code != 503 {
		t.Fatalf("unreachable: %d", w.Code)
	}
	var n int
	m.s.db.QueryRow(`SELECT count(*) FROM deployment`).Scan(&n)
	if n != 0 || len(m.drv.calls) != 0 {
		t.Fatalf("%d deployments, driver calls %v", n, m.drv.calls)
	}
	if m.audits("deployment.request", "denied") != 1 || m.audits("deployment.request", "failed") < 2 {
		t.Fatalf("audits denied=%d failed=%d", m.audits("deployment.request", "denied"), m.audits("deployment.request", "failed"))
	}
}

// DOM-009 restart safety: running deployments are failed at startup, queued ones are re-run.
func TestM7_RecoverDeployments(t *testing.T) {
	m := newM7(t)
	m.revision(cm)
	var stk, rev string
	m.s.db.QueryRow(`SELECT id FROM stack`).Scan(&stk)
	m.s.db.QueryRow(`SELECT id FROM revision`).Scan(&rev)
	m.s.db.Exec(`INSERT INTO deployment(id,stack_id,revision_id,action,status,requested_by,created_at,updated_at) VALUES('dep_run',?,?,'apply','running','u','t','t')`, stk, rev)
	m.s.recoverDeployments()
	m.s.wg.Wait()
	got := m.get("dep_run")
	if got["status"] != "failed" || !strings.HasPrefix(got["error"].(string), "interrupted") {
		t.Fatalf("%v", got)
	}
	m.s.db.Exec(`INSERT INTO deployment(id,stack_id,revision_id,action,status,requested_by,created_at,updated_at) VALUES('dep_q',?,?,'apply','queued','u','t','t')`, stk, rev)
	m.s.recoverDeployments()
	m.s.wg.Wait()
	if m.get("dep_q")["status"] != "succeeded" {
		t.Fatal(m.get("dep_q"))
	}
}

// K8S-005: rbac.yaml grants the K8S-021 kinds only the verbs listed, no escalate/bind, no cluster-scoped writes beyond namespaces create.
func TestK8S005_RBACManifest(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/kubernetes/rbac.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var role struct {
		Kind  string
		Rules []struct {
			Resources, Verbs []string
		}
	}
	if err := yaml.Unmarshal([]byte(strings.SplitN(string(raw), "\n---\n", 2)[0]), &role); err != nil || role.Kind != "ClusterRole" {
		t.Fatalf("%v %s", err, role.Kind)
	}
	verbs := map[string]string{}
	for _, r := range role.Rules {
		for _, res := range r.Resources {
			verbs[res] = strings.Join(r.Verbs, ",")
			for _, v := range r.Verbs {
				if v == "escalate" || v == "bind" || v == "*" {
					t.Errorf("%s has verb %s", res, v)
				}
			}
		}
	}
	for _, res := range []string{"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "services", "configmaps", "persistentvolumeclaims",
		"serviceaccounts", "ingresses", "networkpolicies", "horizontalpodautoscalers", "poddisruptionbudgets"} {
		if verbs[res] != "get,list,watch,create,patch,delete" {
			t.Errorf("%s verbs = %q", res, verbs[res])
		}
	}
	if verbs["pods"] != "get,list,watch" || verbs["namespaces"] != "get,create" || len(verbs) != 15 {
		t.Errorf("pods=%q namespaces=%q resources=%d", verbs["pods"], verbs["namespaces"], len(verbs))
	}
}
