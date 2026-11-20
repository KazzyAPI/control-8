package api

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/inventory"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

type mutCache struct {
	mu   sync.Mutex
	objs []map[string]any
}

func (c *mutCache) set(o ...map[string]any) { c.mu.Lock(); c.objs = o; c.mu.Unlock() }
func (c *mutCache) Objects(string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objs
}
func (c *mutCache) Sync() string { return "watching" }

var _ rt.Cache = (*mutCache)(nil)

type m10 struct {
	*m7
	cache   *mutCache
	eng     *inventory.Engine
	beat    chan time.Time
	clock   atomic.Int64 // unix ms
	stackID string
}

func (m *m10) advance(d time.Duration) { m.clock.Add(d.Milliseconds()) }

func newM10(t *testing.T) *m10 {
	m := &m10{m7: newM7(t), cache: &mutCache{}, beat: make(chan time.Time, 8)}
	m.clock.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
	m.eng = inventory.New(m.s.db, time.Millisecond)
	WithInventory(m.eng)(m.s)
	m.eng.SetCache("home", m.cache)
	m.s.now = func() time.Time { return time.UnixMilli(m.clock.Load()).UTC() }
	m.s.tick = func(time.Duration) (<-chan time.Time, func()) { return m.beat, func() {} }
	m.s.progress = events.NewCoalescer(0) // coalescing itself is covered by TestEVT005_Coalescer
	m.s.db.QueryRow(`SELECT id FROM stack WHERE slug='s1'`).Scan(&m.stackID)
	return m
}

func (m *m10) revisionWith(manifests, profile string) {
	b, _ := json.Marshal(map[string]any{"safety_profile": profile, "manifests": manifests, "rollout_timeout_seconds": 30})
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/revisions", string(b), m.op); w.Code != 201 {
		m.t.Fatal(w.Body)
	}
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatal("timed out waiting for " + what)
}

func (m *m10) rollout(id string) map[string]any {
	r, _ := m.get(id)["rollout"].(map[string]any)
	return r
}

func (m *m10) workload(id string) map[string]any {
	if ws, _ := m.rollout(id)["workloads"].([]any); len(ws) > 0 {
		return ws[0].(map[string]any)
	}
	return map[string]any{}
}

func webDep(rev, replicas, updated, available int, extra map[string]any) map[string]any {
	st := map[string]any{"observedGeneration": 1, "replicas": updated, "updatedReplicas": updated, "readyReplicas": available, "availableReplicas": available}
	for k, v := range extra {
		st[k] = v
	}
	return map[string]any{"kind": "Deployment", "metadata": map[string]any{"name": "web", "generation": 1, "labels": map[string]any{"control-8/revision": string(rune('0' + rev))}},
		"spec": map[string]any{"replicas": replicas}, "status": st}
}

const web3 = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  replicas: 3\n  selector: {matchLabels: {app: web}}\n  template:\n    metadata: {labels: {app: web}}\n    spec: {containers: [{name: web, image: busybox}]}\n"

func (f *fakeDriver) has(c string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.calls {
		if x == c {
			return true
		}
	}
	return false
}

func (m *m10) audited(action string) bool {
	var n int
	m.s.db.QueryRow(`SELECT count(*) FROM audit_event WHERE action=?`, action).Scan(&n)
	return n > 0
}

// K8S-063, EVT-004, R-8, DOM-009: 3-replica rollout emits rising progress to a viewer (not to an outsider's
// filter), becomes healthy, prunes only afterwards, audits rollout.healthy.
func TestK8S063_RolloutProgressAndHealthy(t *testing.T) {
	m := newM10(t)
	m.revisionWith(web3, "as_is")
	sub, _, _ := m.s.bus.Subscribe(nil)
	_, d := m.deploy(m.op, 1)
	id := d["id"].(string)
	waitFor(t, "waiting", func() bool { return m.rollout(id)["state"] == "waiting" })
	for _, u := range []int{1, 2} {
		m.cache.set(webDep(1, 3, u, u-1, nil))
		m.eng.Enqueue(m.stackID)
		waitFor(t, "progress", func() bool { return m.workload(id)["updated"] == float64(u) })
	}
	if m.drv.has("prune") {
		t.Fatal("pruned before healthy")
	}
	m.cache.set(webDep(1, 3, 3, 3, nil))
	m.eng.Enqueue(m.stackID)
	m.s.wg.Wait()
	got := m.get(id)
	if got["status"] != "succeeded" || m.rollout(id)["result"] != "healthy" || m.rollout(id)["state"] != "done" || !m.audited("rollout.healthy") {
		t.Fatalf("%v", got)
	}
	var ups []float64
	for len(sub.C) > 0 {
		ev := <-sub.C
		if ev.Type != "deployment.updated" {
			continue
		}
		b, _ := json.Marshal(ev.Data)
		var x struct {
			Rollout struct{ Workloads []struct{ Updated float64 } }
		}
		json.Unmarshal(b, &x)
		if strings.Contains(string(b), `"output"`) {
			t.Fatal("event carries output")
		}
		if len(x.Rollout.Workloads) > 0 {
			ups = append(ups, x.Rollout.Workloads[0].Updated)
		}
	}
	if len(ups) < 3 || ups[0] > ups[1] || ups[1] > ups[2] || ups[len(ups)-1] != 3 {
		t.Fatalf("progress %v", ups)
	}
}

// K8S-063, 08 immediate-fail, K8S-053 (first apply fails like as_is): unhealthy at timeout (fake clock),
// stack failed, nothing pruned, rollout.failed audited and the failure reason stored.
func TestK8S063_TimeoutFailsAsIs(t *testing.T) {
	m := newM10(t)
	m.revisionWith(web3, "as_is")
	_, d := m.deploy(m.op, 1)
	id := d["id"].(string)
	m.cache.set(webDep(1, 3, 3, 0, nil))
	waitFor(t, "waiting", func() bool { return m.workload(id)["updated"] == float64(3) })
	m.advance(31 * time.Second)
	m.beat <- time.Time{}
	m.s.wg.Wait()
	got := m.get(id)
	if got["status"] != "failed" || m.rollout(id)["result"] != "failed" || !strings.Contains(got["error"].(string), "rollout_timeout") ||
		m.stack()["state"] != "failed" || !m.audited("rollout.failed") || m.drv.has("prune") {
		t.Fatalf("%v / %v", got, m.stack())
	}
}

// K8S-063: ProgressDeadlineExceeded fails at once, long before the timeout.
func TestK8S063_ImmediateFail(t *testing.T) {
	m := newM10(t)
	m.revisionWith(web3, "as_is")
	_, d := m.deploy(m.op, 1)
	id := d["id"].(string)
	waitFor(t, "waiting", func() bool { return m.rollout(id)["state"] == "waiting" })
	m.cache.set(webDep(1, 3, 3, 0, map[string]any{"conditions": []any{map[string]any{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded"}}}))
	m.eng.Enqueue(m.stackID)
	m.s.wg.Wait()
	if got := m.get(id); got["status"] != "failed" || !strings.Contains(got["error"].(string), "ProgressDeadlineExceeded") {
		t.Fatalf("%v", got)
	}
}

// K8S-063: a Job that completes counts as healthy.
func TestK8S063_JobCompletes(t *testing.T) {
	m := newM10(t)
	m.revisionWith("apiVersion: batch/v1\nkind: Job\nmetadata: {name: mig}\nspec:\n  template:\n    spec: {restartPolicy: Never, containers: [{name: m, image: busybox}]}\n", "as_is")
	_, d := m.deploy(m.op, 1)
	id := d["id"].(string)
	waitFor(t, "waiting", func() bool { return m.rollout(id)["state"] == "waiting" })
	m.cache.set(map[string]any{"kind": "Job", "metadata": map[string]any{"name": "mig", "labels": map[string]any{"control-8/revision": "1"}},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Complete", "status": "True"}}}})
	m.eng.Enqueue(m.stackID)
	m.s.wg.Wait()
	if got := m.get(id); got["status"] != "succeeded" || m.rollout(id)["result"] != "healthy" {
		t.Fatalf("%v", got)
	}
}
