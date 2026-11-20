package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
	"github.com/KazzyAPI/control-8/internal/targets"
)

const manifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: 3
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec: {containers: [{name: web, image: "ghcr.io/acme/web:1.0"}]}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: cfg}
`

// fixture returns the rendered objects of revision 2 and the inventory.
func fixture(t *testing.T) Input {
	objs, err := k8s.Parse(manifest)
	if err != nil {
		t.Fatal(err)
	}
	rendered, inv := k8s.Render(objs, "stk_1", "shop", 2, "as_is")
	return Input{Inventory: inv, Rendered: rendered, Revision: 2}
}

// cached is the Deployment as the cluster reports it: rendered + status.
func cached(in Input, ready int64, mut func(map[string]any)) []map[string]any {
	d := clone(in.Rendered[0])
	if k8s.Kind(d) != "Deployment" {
		d = clone(in.Rendered[1])
	}
	d["status"] = map[string]any{"readyReplicas": ready, "updatedReplicas": int64(3), "observedGeneration": int64(1)}
	d["metadata"].(map[string]any)["generation"] = int64(1)
	if mut != nil {
		mut(d)
	}
	pod := map[string]any{"kind": "Pod", "metadata": map[string]any{"labels": map[string]any{"app": "web"}},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			"containerStatuses": []any{map[string]any{"imageID": "ghcr.io/acme/web@sha256:aa"}}}}
	return []map[string]any{d, pod}
}

func kinds(r Result) (out []string) {
	for _, d := range r.Drift {
		out = append(out, d.Kind)
	}
	return
}

// K8S-062, K8S-061: Compute is a pure function over cached objects; one case per drift kind and health.
func TestK8S062_ComputeTable(t *testing.T) {
	base := fixture(t)
	cases := []struct {
		name   string
		mut    func(*Input)
		health string
		drift  []string
		state  string
	}{
		{"healthy", func(in *Input) { in.Cached = cached(*in, 3, nil) }, "healthy", nil, "running"},
		{"scaled down is degraded", func(in *Input) { in.Cached = cached(*in, 1, nil) }, "degraded", nil, "degraded"},
		{"rolling out is progressing service", func(in *Input) {
			in.Cached = cached(*in, 1, func(d map[string]any) { d["status"].(map[string]any)["updatedReplicas"] = int64(1) })
		}, "degraded", nil, "progressing"},
		{"deleted deployment", func(in *Input) { in.Cached = nil }, "drifted", []string{"service_missing"}, "missing"},
		{"image set by hand", func(in *Input) {
			in.Cached = cached(*in, 3, func(d map[string]any) {
				d["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "ghcr.io/acme/web:2.0"
			})
		}, "drifted", []string{"image_mismatch"}, "running"},
		{"digest pin alone is no drift", func(in *Input) {
			in.Cached = cached(*in, 3, func(d map[string]any) {
				d["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = "ghcr.io/acme/web:1.0@sha256:aa"
			})
		}, "healthy", nil, "running"},
		{"old revision label", func(in *Input) {
			in.Cached = cached(*in, 3, func(d map[string]any) {
				d["metadata"].(map[string]any)["labels"].(map[string]any)["control-8/revision"] = "1"
			})
		}, "drifted", []string{"label_mismatch"}, "running"},
		{"extra labelled workload", func(in *Input) {
			in.Cached = cached(*in, 3, nil)
			x := clone(in.Cached[0])
			x["metadata"].(map[string]any)["name"] = "zrogue"
			in.Cached = append(in.Cached, x)
		}, "drifted", []string{"service_extra"}, "running"},
		{"progressing beats degraded", func(in *Input) { in.Cached = cached(*in, 1, nil); in.Progressing = true }, "progressing", nil, "degraded"},
		{"frozen beats progressing", func(in *Input) { in.Cached = cached(*in, 3, nil); in.Progressing, in.Frozen = true, true }, "frozen", nil, "running"},
		{"unreachable beats frozen", func(in *Input) { in.Cached = cached(*in, 3, nil); in.Frozen, in.Unreachable = true, true }, "unknown", nil, "running"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mut(&in)
			r := Compute(in)
			if r.Health != c.health || !slices.Equal(kinds(r), c.drift) || r.Services[0].State != c.state || r.Services[0].Name != "Deployment/web" {
				t.Fatalf("health=%s drift=%v svc=%+v", r.Health, kinds(r), r.Services)
			}
		})
	}
	// image_id comes from a ready pod selected by the workload selector (K8S-062)
	in := base
	in.Cached = cached(in, 3, nil)
	if id := Compute(in).Services[0].ImageID; id == nil || *id != "ghcr.io/acme/web@sha256:aa" {
		t.Fatalf("image_id %v", id)
	}
}

type fakeCache struct {
	mu   sync.Mutex
	objs map[string][]map[string]any
	sync string
}

func (c *fakeCache) Objects(id string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.objs[id]
}
func (c *fakeCache) Sync() string { return c.sync }

type fakeDrv struct {
	rt.Driver
	mu      sync.Mutex
	starts  int
	cache   *fakeCache
	stopped chan struct{}
}

func (d *fakeDrv) Watch(ctx context.Context, o rt.WatchOptions) (rt.Cache, error) {
	d.mu.Lock()
	d.starts++
	d.mu.Unlock()
	return d.cache, nil
}
func (d *fakeDrv) n() int { d.mu.Lock(); defer d.mu.Unlock(); return d.starts }

type fakeV struct {
	mu  sync.Mutex
	err error
}

func (f *fakeV) set(e error) { f.mu.Lock(); f.err = e; f.mu.Unlock() }
func (f *fakeV) ServerVersion() (*version.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &version.Info{GitVersion: "v1"}, f.err
}

func newDB(t *testing.T, in Input) *Engine {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`INSERT INTO target(id,name,kind,environment_tier) VALUES('tgt_1','home','kubernetes','non-prod')`,
		`INSERT INTO namespace(id,slug,display_name,environment_tier,created_at,updated_at) VALUES('ns_1','shop','S','non-prod','t','t')`,
		`INSERT INTO asset(id,namespace_id,slug,name,managed_by,created_at,updated_at) VALUES('ast_1','ns_1','web','Web','control8','t','t')`,
		`INSERT INTO stack(id,namespace_id,asset_id,target_id,slug,runtime,k8s_namespace,state,desired_revision,created_at,updated_at) VALUES('stk_1','ns_1','ast_1','tgt_1','web','kubernetes','shop','active',2,'t','t')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err, q)
		}
	}
	var inv, rend []byte
	inv, _ = jsonMarshal(in.Inventory)
	rend, _ = jsonMarshal(in.Rendered)
	if _, err := db.Exec(`INSERT INTO revision(id,stack_id,number,safety_profile,rendered,inventory,rollout_timeout_seconds,policy_result,created_by,created_at) VALUES('rev_2','stk_1',2,'as_is',?,?,300,'{}','u','t')`, string(rend), string(inv)); err != nil {
		t.Fatal(err)
	}
	return New(db, 5*time.Millisecond)
}

// K8S-061: recompute writes service rows + drift + health and signals Changed only when something changed.
func TestK8S061_EngineWritesOnlyOnChange(t *testing.T) {
	in := fixture(t)
	e := newDB(t, in)
	c := &fakeCache{objs: map[string][]map[string]any{"stk_1": cached(in, 3, nil)}, sync: "watching"}
	e.SetCache("home", c)
	changed := make(chan string, 10)
	e.Changed = func(id string) { changed <- id }
	e.Enqueue("stk_1")
	e.Enqueue("stk_1") // coalesced
	wait := func() {
		select {
		case <-changed:
		case <-time.After(2 * time.Second):
			t.Fatal("no change")
		}
	}
	wait()
	var health string
	var n int
	e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&health)
	e.DB.QueryRow(`SELECT count(*) FROM service WHERE stack_id='stk_1' AND ready_replicas=3`).Scan(&n)
	if health != "healthy" || n != 1 || len(changed) != 0 {
		t.Fatalf("health=%s rows=%d pending=%d", health, n, len(changed))
	}
	e.Recompute("stk_1") // nothing changed: no event
	c.mu.Lock()
	c.objs["stk_1"] = cached(in, 1, nil)
	c.mu.Unlock()
	e.Recompute("stk_1")
	wait()
	e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&health)
	if health != "degraded" || len(changed) != 0 {
		t.Fatalf("health=%s pending=%d", health, len(changed))
	}
	// K8S-065: unreachable keeps services, flips health, publishes once; recovery recomputes
	e.MarkUnknown("home")
	wait()
	e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&health)
	e.DB.QueryRow(`SELECT count(*) FROM service WHERE stack_id='stk_1'`).Scan(&n)
	if health != "unknown" || n != 1 {
		t.Fatalf("health=%s rows=%d", health, n)
	}
	e.MarkUnknown("home")
	e.Recompute("stk_1")
	wait()
	e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&health)
	if health != "degraded" || len(changed) != 0 {
		t.Fatalf("health=%s", health)
	}
	e.Stop()
}

// K8S-003, K8S-064, K8S-065, K8S-060: sync state follows the cache, "down" on unreachable, informers restart on recovery.
func TestK8S064_ManagerSyncDownRecovery(t *testing.T) {
	in := fixture(t)
	e := newDB(t, in)
	drv := &fakeDrv{cache: &fakeCache{objs: map[string][]map[string]any{"stk_1": cached(in, 3, nil)}, sync: "polling"}}
	v := &fakeV{}
	tc := targets.NewChecker([]targets.Spec{{ID: "tgt_1", Name: "home", Kind: "kubernetes", Tier: "non-prod", KubeAuth: "in_cluster"}},
		func(targets.Spec) (targets.Versioner, error) { return v, nil })
	var mu sync.Mutex
	var syncs []string
	tc.Publish = func(t targets.Target) { mu.Lock(); syncs = append(syncs, t.Sync); mu.Unlock() }
	m := &Manager{E: e, Targets: tc, Drivers: func(string) (rt.Driver, error) { return drv, nil }, Resync: time.Hour, Every: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	until := func(what string, f func() bool) {
		t.Helper()
		for i := 0; i < 300 && !f(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !f() {
			t.Fatal("timeout: " + what)
		}
	}
	tc.Check("home")
	until("polling", func() bool { x, _ := tc.Get("home"); return x.Sync == "polling" })
	until("health", func() bool {
		var h string
		e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&h)
		return h == "healthy"
	})
	v.set(errors.New("connection refused"))
	tc.Check("home")
	until("down+unknown", func() bool {
		x, _ := tc.Get("home")
		var h string
		e.DB.QueryRow(`SELECT health FROM stack WHERE id='stk_1'`).Scan(&h)
		return x.Sync == "down" && !x.Reachable && h == "unknown"
	})
	v.set(nil)
	tc.Check("home")
	until("restart", func() bool { x, _ := tc.Get("home"); return drv.n() == 2 && x.Sync == "polling" })
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop")
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func clone(o map[string]any) map[string]any { return kruntime.DeepCopyJSON(o) }
