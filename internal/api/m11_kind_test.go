//go:build k8s

package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/KazzyAPI/control-8/internal/inventory"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

// failNthApply makes the n-th Apply fail: the M11 "rollback also fails" test hook.
type failNthApply struct {
	rt.Driver
	n, at atomic.Int32
}

func (f *failNthApply) Apply(ctx context.Context, p rt.Plan) (string, error) {
	if f.n.Add(1) == f.at.Load() {
		return "", errors.New("test hook: apply failed")
	}
	return f.Driver.Apply(ctx, p)
}

// M11 acceptance on kind (K8S-050..055): good tag -> healthy N+1; non-existent tag -> rolled back to N with the
// old image running; rollback also failing -> rollback_failed, frozen, 409 until an admin deploy succeeds;
// repo change -> 400. Same-repository tags of registry.k8s.io/pause stand in for a local registry (D-85).
func TestKindM11_ManagedRollout(t *testing.T) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	dyn, _ := dynamic.NewForConfig(cfg)
	m := newM10(t)
	drv := &failNthApply{Driver: k8s.NewDriver(dyn)}
	m.s.drivers = func(string) (rt.Driver, error) { return drv, nil }
	m.s.now, m.s.tick = time.Now, realTick
	m.eng = inventory.New(m.s.db, time.Second)
	WithInventory(m.eng)(m.s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := drv.Driver.Watch(ctx, rt.WatchOptions{Resync: time.Minute, Poll: 5 * time.Second, Enqueue: m.eng.Enqueue})
	if err != nil {
		t.Fatal(err)
	}
	m.eng.SetCache("home", c)
	t.Cleanup(func() {
		dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Delete(context.Background(), "a", metav1.DeleteOptions{})
	})
	m.revisionWith(strings.Replace(web3, "image: busybox", "image: registry.k8s.io/pause:3.9", 1), "managed_rollout")
	m.revisionWith(strings.Replace(web3, "image: busybox", "image: registry.k8s.io/pause:3.9", 1), "managed_rollout") // rev 2 = same, used to re-deploy as admin
	if d := m.deployRev(1, "managed_rollout"); d["status"] != "succeeded" {
		t.Fatal(d)
	}
	if code, d := m.imageUpdate(m.op, "registry.k8s.io/pause:3.10"); code != 202 || d["status"] != "succeeded" || m.rollout(d["id"].(string))["result"] != "healthy" {
		t.Fatal(code, d)
	}
	if code, d := m.imageUpdate(m.op, "quay.io/other/pause:1"); code != 400 {
		t.Fatal("repo change", code, d)
	}
	start := time.Now()
	code, d := m.imageUpdate(m.op, "registry.k8s.io/pause:no-such-tag")
	ro := m.rollout(d["id"].(string))
	if code != 202 || d["status"] != "failed" || ro["result"] != "rolled_back" || m.stack()["state"] != "active" || time.Since(start) > 3*time.Minute {
		t.Fatalf("%d %v %v", code, d, ro)
	}
	// rollback also failing: the next image update's second Apply (the rollback) is the hooked one
	drv.n.Store(0)
	drv.at.Store(2)
	code, d = m.imageUpdate(m.op, "registry.k8s.io/pause:no-such-tag-2")
	if ro = m.rollout(d["id"].(string)); code != 202 || ro["result"] != "rollback_failed" || m.stack()["rollout_frozen"] != true {
		t.Fatalf("%d %v %v", code, d, ro)
	}
	if code, _ := m.imageUpdate(m.op, "registry.k8s.io/pause:3.9"); code != 409 {
		t.Fatal("frozen", code)
	}
	drv.at.Store(0)
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", `{"revision":1,"confirm_safety_profile":"managed_rollout","confirm_environment_tier":"non-prod"}`, m.aa); w.Code != 202 {
		t.Fatal(w.Body)
	}
	m.s.wg.Wait()
	if m.stack()["rollout_frozen"] != false {
		t.Fatal("not cleared")
	}
}
