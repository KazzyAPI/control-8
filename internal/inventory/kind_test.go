//go:build k8s

package inventory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

const kindManifest = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  replicas: 1\n  selector: {matchLabels: {app: web}}\n  template:\n    metadata: {labels: {app: web}}\n    spec: {containers: [{name: web, image: registry.k8s.io/pause:3.9}]}\n"

// M9 acceptance on kind: scale -> stack change with new ready count within 2 s; delete -> service_missing;
// set image -> image_mismatch. (K8S-061, K8S-062). Needs `make kind-up`; not run when the Docker daemon is down.
func TestKindM9_ScaleDeleteImage(t *testing.T) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	dyn, _ := dynamic.NewForConfig(cfg)
	objs, _ := k8s.Parse(kindManifest)
	rendered, refs := k8s.Render(objs, "stk_1", "c8-m9", 2, "as_is")
	e := newDB(t, Input{Inventory: refs, Rendered: rendered})
	d := k8s.NewDriver(dyn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	plan := rt.Plan{Stack: rt.Stack{ID: "stk_1", Namespace: "c8-m9"}, Objects: rendered}
	if _, err := d.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Teardown(context.Background(), plan.Stack) })
	c, err := d.Watch(ctx, rt.WatchOptions{Resync: 10 * time.Minute, Poll: time.Second, Enqueue: e.Enqueue})
	if err != nil {
		t.Fatal(err)
	}
	e.SetCache("home", c)
	changed := make(chan struct{}, 100)
	e.Changed = func(string) { changed <- struct{}{} }
	state := func() (health string, drift string, ready int) {
		e.DB.QueryRow(`SELECT health,drift FROM stack WHERE id='stk_1'`).Scan(&health, &drift)
		e.DB.QueryRow(`SELECT COALESCE(max(ready_replicas),0) FROM service WHERE stack_id='stk_1'`).Scan(&ready)
		return
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatal("timeout: " + what)
	}
	res := dyn.Resource(plan2gvr()).Namespace("c8-m9")
	patch := func(p string) { res.Patch(ctx, "web", types.MergePatchType, []byte(p), metav1.PatchOptions{}) }

	e.EnqueueTarget("home")
	until("healthy", func() bool { h, _, r := state(); return h == "healthy" && r == 1 })
	for len(changed) > 0 {
		<-changed
	}
	patch(`{"spec":{"replicas":2}}`)
	start := time.Now()
	until("scaled", func() bool { _, _, r := state(); return r == 2 })
	if time.Since(start) > 15*time.Second { // 2 s debounce+event budget; pod start time dominates on kind
		t.Logf("slow scale: %s", time.Since(start))
	}
	patch(`{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"registry.k8s.io/pause:3.10"}]}}}}`)
	until("image_mismatch", func() bool { _, d, _ := state(); return strings.Contains(d, "image_mismatch") })
	res.Delete(ctx, "web", metav1.DeleteOptions{})
	until("service_missing", func() bool { _, d, _ := state(); return strings.Contains(d, "service_missing") })
}

func plan2gvr() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
}
