//go:build k8s

package api

import (
	"context"
	"os"
	"strings"
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

// M10 acceptance on kind (K8S-063): a pod that never becomes ready fails the as_is deployment within the
// rollout timeout; a 3-replica rollout reports rising progress; a Job that completes is healthy.
func TestKindM10_RolloutHealth(t *testing.T) {
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
	drv := k8s.NewDriver(dyn)
	m.s.drivers = func(string) (rt.Driver, error) { return drv, nil }
	m.s.now, m.s.tick = time.Now, realTick // real clock against a real cluster
	m.eng = inventory.New(m.s.db, time.Second)
	WithInventory(m.eng)(m.s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := drv.Watch(ctx, rt.WatchOptions{Resync: time.Minute, Poll: 5 * time.Second, Enqueue: m.eng.Enqueue})
	if err != nil {
		t.Fatal(err)
	}
	m.eng.SetCache("home", c)
	t.Cleanup(func() {
		dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Delete(context.Background(), "a", metav1.DeleteOptions{})
	})
	run := func(manifests string) map[string]any {
		m.revisionWith(manifests, "as_is")
		var n int
		m.s.db.QueryRow(`SELECT max(number) FROM revision`).Scan(&n)
		body := strings.Replace(dep, "%d", string(rune('0'+n)), 1)
		w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments", body, m.op)
		if w.Code != 202 {
			t.Fatal(w.Body)
		}
		m.s.wg.Wait()
		var id string
		m.s.db.QueryRow(`SELECT id FROM deployment ORDER BY rowid DESC LIMIT 1`).Scan(&id)
		return m.get(id)
	}
	good := strings.Replace(web3, "image: busybox", "image: registry.k8s.io/pause:3.9", 1)
	if got := run(good); got["status"] != "succeeded" || m.rollout(got["id"].(string))["result"] != "healthy" {
		t.Fatalf("3 replicas: %v", got)
	}
	job := "apiVersion: batch/v1\nkind: Job\nmetadata: {name: ok}\nspec:\n  template:\n    spec: {restartPolicy: Never, containers: [{name: m, image: busybox, command: [true]}]}\n"
	if got := run(job); got["status"] != "succeeded" {
		t.Fatalf("job: %v", got)
	}
	bad := strings.Replace(web3, "image: busybox", "image: busybox, command: [sh, -c, 'exit 1']", 1)
	start := time.Now()
	got := run(bad)
	if got["status"] != "failed" || m.rollout(got["id"].(string))["result"] != "failed" || time.Since(start) > 90*time.Second || m.stack()["state"] != "failed" {
		t.Fatalf("failing image: %v", got)
	}
}
