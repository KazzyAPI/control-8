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

	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

// M7 acceptance: a real apply through the API on kind; the SSE-visible sequence is queued -> running -> succeeded.
func TestKindM7_APIApplyEvents(t *testing.T) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	dyn, _ := dynamic.NewForConfig(cfg)
	m := newM7(t)
	m.s.drivers = func(string) (rt.Driver, error) { return k8s.NewDriver(dyn), nil }
	t.Cleanup(func() {
		delNS(dyn)
	})
	m.revision(cm)
	sub, _, _ := m.s.bus.Subscribe(nil)
	code, d := m.deploy(m.op, 1)
	m.s.wg.Wait()
	got := m.get(d["id"].(string))
	if code != 202 || got["status"] != "succeeded" {
		t.Fatalf("%d %v", code, got)
	}
	var seq []string
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Type == "deployment.updated" {
			seq = append(seq, ev.Data.(map[string]any)["status"].(string))
		}
	}
	if strings.Join(seq, ",") != "queued,running,succeeded" {
		t.Fatalf("events %v", seq)
	}
}

// delNS deletes ns "a" and waits until it is gone: a Terminating namespace rejects creates as Forbidden,
// which broke the next kind test that reused "a".
func delNS(dyn dynamic.Interface) {
	r := dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"})
	r.Delete(context.Background(), "a", metav1.DeleteOptions{})
	for i := 0; i < 120; i++ {
		if _, err := r.Get(context.Background(), "a", metav1.GetOptions{}); err != nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
