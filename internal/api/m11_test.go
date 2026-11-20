package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

const imgBody = `{"workload":"Deployment/web","container":"web","image":"%s","reason":"patch","confirm_environment_tier":"non-prod"}`

// script outcomes per Apply call: "ok" = the plan's revision becomes healthy, "bad" = ProgressDeadlineExceeded, "err" = Apply fails.
func (m *m10) script(out ...string) {
	var mu sync.Mutex
	n := 0
	m.drv.hook = func(p rt.Plan) error {
		mu.Lock()
		o := out[min(n, len(out)-1)] // the last outcome repeats
		n++
		mu.Unlock()
		rev := 0
		fmt.Sscan(fmt.Sprint(k8s.Dig(p.Objects[0], "metadata", "labels", "control-8/revision")), &rev)
		switch o {
		case "ok":
			m.cache.set(webDep(rev, 3, 3, 3, nil))
		case "bad":
			m.cache.set(webDep(rev, 3, 3, 0, map[string]any{"conditions": []any{map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}}))
		case "err":
			return errors.New("boom")
		}
		return nil
	}
}

func (m *m10) deployRev(n int, profile string) map[string]any {
	b := fmt.Sprintf(`{"revision":%d,"confirm_safety_profile":%q,"confirm_environment_tier":"non-prod","reason":"go"}`, n, profile)
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", b, m.op)
	if w.Code != 202 {
		m.t.Fatal(w.Body)
	}
	m.s.wg.Wait()
	var d map[string]any
	json.Unmarshal(w.Body.Bytes(), &d)
	return m.get(d["id"].(string))
}

func (m *m10) imageUpdate(h map[string]string, image string) (int, map[string]any) {
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/image-updates", fmt.Sprintf(imgBody, image), h)
	var d map[string]any
	json.Unmarshal(w.Body.Bytes(), &d)
	if w.Code == 202 {
		m.s.wg.Wait()
		d = m.get(d["id"].(string))
	}
	return w.Code, d
}

// firstManaged deploys revision 1 (managed_rollout, healthy) so later updates have a predecessor.
func (m *m10) firstManaged() {
	m.revisionWith(web3, "managed_rollout")
	m.script("ok")
	if d := m.deployRev(1, "managed_rollout"); d["status"] != "succeeded" {
		m.t.Fatal(d)
	}
}

func (m *m10) calls(c string) (n int) {
	m.drv.mu.Lock()
	defer m.drv.mu.Unlock()
	for _, x := range m.drv.calls {
		if x == c {
			n++
		}
	}
	return
}

// K8S-052..055, R-8: image update table over apply/health outcomes (fake driver). Script = outcomes of the
// Apply calls after the healthy first deploy: new revision, then rollback.
func TestK8S053_055_RollbackTable(t *testing.T) {
	for _, c := range []struct {
		name, script, result, state, status string
		desired                             float64
		frozen                              bool
		applies                             int
	}{
		{"healthy", "ok", "healthy", "active", "succeeded", 2, false, 1},
		{"rolled_back", "bad ok", "rolled_back", "active", "failed", 1, false, 2},
		{"rollback_unhealthy", "bad bad", "rollback_failed", "failed", "failed", 1, true, 2},
		{"rollback_apply_error", "bad err", "rollback_failed", "failed", "failed", 1, true, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newM10(t)
			m.firstManaged()
			m.script(strings.Fields(c.script)...)
			m.drv.mu.Lock()
			m.drv.calls = nil
			m.drv.mu.Unlock()
			code, d := m.imageUpdate(m.op, "busybox:1.36")
			if code != 202 || d["trigger"] != "image_update" || d["revision"] != float64(2) {
				t.Fatal(code, d)
			}
			ro := m.rollout(d["id"].(string))
			st := m.stack()
			if d["status"] != c.status || ro["result"] != c.result || ro["state"] != "done" || st["state"] != c.state ||
				st["desired_revision"] != c.desired || st["rollout_frozen"] != c.frozen || m.calls("apply") != c.applies {
				t.Fatalf("%v\n%v\n%v", d, ro, st)
			}
			if c.result == "rolled_back" && (ro["rolled_back_to"] != float64(1) || !m.audited("rollout.rollback") || m.calls("prune") != 1) {
				t.Fatalf("%v", ro)
			}
			if c.frozen && (st["health"] != "frozen" || !m.audited("stack.rollout_frozen")) {
				t.Fatalf("%v", st)
			}
			if !c.frozen && m.audited("stack.rollout_frozen") {
				t.Fatal("frozen audit without freeze")
			}
		})
	}
}

// K8S-053: no predecessor (first apply) and as_is revisions are never rolled back.
func TestK8S053_NoRollbackWithoutManagedPredecessor(t *testing.T) {
	m := newM10(t)
	m.revisionWith(web3, "managed_rollout")
	m.script("bad")
	d := m.deployRev(1, "managed_rollout")
	if ro := m.rollout(d["id"].(string)); d["status"] != "failed" || ro["result"] != "failed" || m.calls("apply") != 1 || m.stack()["state"] != "failed" {
		t.Fatalf("%v %v", d, ro)
	}
	m = newM10(t)
	m.firstManaged()
	m.revisionWith(strings.Replace(web3, "replicas: 3", "replicas: 4", 1), "as_is")
	m.script("bad")
	d = m.deployRev(2, "as_is")
	if ro := m.rollout(d["id"].(string)); ro["result"] != "failed" || m.calls("apply") != 2 || m.stack()["desired_revision"] != float64(1) {
		t.Fatalf("%v %v", d, ro)
	}
}

// K8S-050, K8S-052, K8S-055, API-003, DOM-009: validation, revision N+1, freeze and admin clear.
func TestK8S050_ImageUpdateRules(t *testing.T) {
	m := newM10(t)
	if c, _ := m.imageUpdate(m.op, "busybox:1"); c != 409 { // draft stack, no revision
		t.Fatal(c)
	}
	m.firstManaged()
	for _, c := range []struct {
		h     map[string]string
		image string
		want  int
	}{
		{m.viewer, "busybox:1", 403}, {m.other, "busybox:1", 404},
		{m.op, "alpine:3", 400}, {m.op, "busybox:1.36@sha256:" + strings.Repeat("a", 64), 202},
	} {
		if got, d := m.imageUpdate(c.h, c.image); got != c.want {
			t.Fatal(c.image, got, d)
		}
	}
	var base int
	var origin, input string
	m.s.db.QueryRow(`SELECT origin,base_revision,manifests_input FROM revision WHERE number=2`).Scan(&origin, &base, &input)
	if origin != "image_update" || base != 1 || !strings.Contains(input, "busybox:1.36@sha256:") || !m.audited("image_update.request") {
		t.Fatal(origin, base, input)
	}
	for _, b := range []string{`{"workload":"Deployment/nope","container":"web","image":"busybox:2","reason":"r","confirm_environment_tier":"non-prod"}`,
		`{"workload":"Deployment/web","container":"nope","image":"busybox:2","reason":"r","confirm_environment_tier":"non-prod"}`} {
		if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/image-updates", b, m.op); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/image-updates", strings.Replace(fmt.Sprintf(imgBody, "busybox:2"), `"patch"`, `""`, 1), m.op); w.Code != 400 {
		t.Fatal("empty reason", w.Code)
	}
	// freeze: rollback also fails
	m.script("bad", "bad")
	if c, d := m.imageUpdate(m.op, "busybox:3"); c != 202 || m.rollout(d["id"].(string))["result"] != "rollback_failed" {
		t.Fatal(c, d)
	}
	if c, _ := m.imageUpdate(m.op, "busybox:4"); c != 409 {
		t.Fatal("frozen image update", c)
	}
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", `{"revision":1,"confirm_safety_profile":"managed_rollout","confirm_environment_tier":"non-prod"}`, m.op); w.Code != 409 || !strings.Contains(w.Body.String(), "rollout_frozen") {
		t.Fatal("operator deploy on frozen", w.Code, w.Body)
	}
	m.script("ok")
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", `{"revision":1,"confirm_safety_profile":"managed_rollout","confirm_environment_tier":"non-prod"}`, m.aa)
	m.s.wg.Wait()
	if st := m.stack(); w.Code != 202 || st["rollout_frozen"] != false || st["state"] != "active" || !m.audited("stack.rollout_unfrozen") {
		t.Fatal(w.Code, w.Body, st)
	}
	if c, _ := m.imageUpdate(m.op, "busybox:5"); c != 202 {
		t.Fatal("after clear", c)
	}
}

// K8S-050: an as_is stack refuses image updates.
func TestK8S050_AsIsNotAllowed(t *testing.T) {
	m := newM10(t)
	m.revisionWith(web3, "as_is")
	m.script("ok")
	m.deployRev(1, "as_is")
	if c, d := m.imageUpdate(m.op, "busybox:1"); c != 409 || d["code"] != "image_update_not_allowed" {
		t.Fatal(c, d)
	}
}

// GRT-010 (09: manual image update), K8S-050: prod needs a grant; break-glass bypasses and is recorded.
func TestGRT010_ImageUpdateNeedsGrant(t *testing.T) {
	m := newM10(t)
	m.firstManaged()
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	b := fmt.Sprintf(strings.Replace(imgBody, "non-prod", "prod", 1), "busybox:1.36")
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/image-updates", b, m.op); w.Code != 403 || !strings.Contains(w.Body.String(), "grant_required") {
		t.Fatal(w.Code, w.Body)
	}
	m.script("ok")
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/image-updates", b, m.admin); w.Code != 202 || !strings.Contains(w.Body.String(), "break_glass") {
		t.Fatal(w.Code, w.Body)
	}
	m.s.wg.Wait()
}

// EVT-004, R-8: the rollout shows rolling_back then done on the stream; a viewer of another namespace sees nothing.
func TestK8S055_RollbackEvents(t *testing.T) {
	m := newM10(t)
	m.firstManaged()
	sub, _, _ := m.s.bus.Subscribe(nil)
	m.script("bad", "ok")
	m.imageUpdate(m.op, "busybox:1.36")
	var states []string
	var rev bool
	for len(sub.C) > 0 {
		ev := <-sub.C
		switch ev.Type {
		case "revision.created":
			rev = true
		case "deployment.updated":
			var x struct{ Rollout struct{ State string } }
			b, _ := json.Marshal(ev.Data)
			json.Unmarshal(b, &x)
			if n := len(states); x.Rollout.State != "" && (n == 0 || states[n-1] != x.Rollout.State) {
				states = append(states, x.Rollout.State)
			}
		}
	}
	got := strings.Join(states, ",")
	if !rev || !strings.Contains(got, "rolling_back,done") {
		t.Fatal(rev, got)
	}
}
