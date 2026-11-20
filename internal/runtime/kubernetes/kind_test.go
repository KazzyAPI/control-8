//go:build k8s

package kubernetes

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

const kindNS = "c8-m7-driver"

func kindDriver(t *testing.T) (*Driver, dynamic.Interface) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dyn.Resource(nsGVR).Delete(context.Background(), kindNS, metav1.DeleteOptions{}) }) // test namespace only
	return NewDriver(dyn), dyn
}

func render(t *testing.T, yamlDocs string) rt.Plan {
	objs, err := Parse(yamlDocs)
	if err != nil {
		t.Fatal(err)
	}
	rendered, _ := Render(objs, "stk_kind", kindNS, 1, "as_is")
	return rt.Plan{Stack: rt.Stack{ID: "stk_kind", Namespace: kindNS}, Objects: rendered}
}

const (
	kcm  = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: %s}\ndata: {a: b}\n"
	kpvc = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data}\nspec: {accessModes: [ReadWriteOnce], resources: {requests: {storage: 1Mi}}}\n"
	kdep = "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  replicas: 1\n  selector: {matchLabels: {app: web}}\n  template:\n    metadata: {labels: {app: web}}\n    spec: {containers: [{name: web, image: registry.k8s.io/pause:3.9}]}\n"
)

func cmDoc(name string) string { return strings.Replace(kcm, "%s", name, 1) }

func get(dyn dynamic.Interface, kind, name string) (*unstructured.Unstructured, error) {
	return dyn.Resource(kindGVR(kind)).Namespace(kindNS).Get(context.Background(), name, metav1.GetOptions{})
}

// K8S-000, K8S-011, K8S-041..043, D-37, D-35 against a real API server (M7 acceptance).
func TestKindM7_ApplyPruneOwnershipTeardown(t *testing.T) {
	d, dyn := kindDriver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	v1 := render(t, cmDoc("cfg")+"---\n"+cmDoc("old")+"---\n"+kpvc+"---\n"+kdep)

	// apply: namespace created, objects labelled, field manager control-8
	if err := d.Precheck(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(ctx, v1); err != nil {
		t.Fatal(err)
	}
	cfg, err := get(dyn, "ConfigMap", "cfg")
	if err != nil || cfg.GetLabels()["control-8/stack"] != "stk_kind" || cfg.GetLabels()["app.kubernetes.io/managed-by"] != "control-8" {
		t.Fatalf("%v %v", err, cfg)
	}
	if mf := cfg.GetManagedFields(); len(mf) == 0 || mf[0].Manager != FieldManager {
		t.Fatalf("managed fields %v", mf)
	}

	// revision 2 drops ConfigMap old: pruned; the PVC is not in the revision either and stays
	v2 := render(t, cmDoc("cfg")+"---\n"+kdep)
	if err := d.Precheck(ctx, v2); err != nil {
		t.Fatal(err)
	}
	d.Apply(ctx, v2)
	if _, err := d.Prune(ctx, v2); err != nil {
		t.Fatal(err)
	}
	if _, err := get(dyn, "ConfigMap", "old"); err == nil {
		t.Fatal("old ConfigMap survived prune")
	}
	if _, err := get(dyn, "PersistentVolumeClaim", "data"); err != nil {
		t.Fatal("PVC was pruned")
	}

	// an unlabelled object with the same name: refused before any write, nothing else created
	foreign := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "foreign"}}}
	dyn.Resource(kindGVR("ConfigMap")).Namespace(kindNS).Create(ctx, foreign, metav1.CreateOptions{})
	err = d.Precheck(ctx, render(t, cmDoc("brand-new")+"---\n"+cmDoc("foreign")))
	if code(err) != "ownership_conflict" {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := get(dyn, "ConfigMap", "brand-new"); err == nil {
		t.Fatal("precheck wrote before refusing")
	}

	// Argo tracking label: argo_managed
	foreign.SetName("argo")
	foreign.SetLabels(map[string]string{"argocd.argoproj.io/instance": "app"})
	dyn.Resource(kindGVR("ConfigMap")).Namespace(kindNS).Create(ctx, foreign, metav1.CreateOptions{})
	if err := d.Precheck(ctx, render(t, cmDoc("argo"))); code(err) != "argo_managed" {
		t.Fatalf("argo: %v", err)
	}

	// kubectl edit of a field we own: the next apply conflicts (never forced)
	web, _ := get(dyn, "Deployment", "web")
	unstructured.SetNestedField(web.Object, int64(3), "spec", "replicas")
	if _, err := dyn.Resource(kindGVR("Deployment")).Namespace(kindNS).Update(ctx, web, metav1.UpdateOptions{FieldManager: "kubectl-edit"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Precheck(ctx, v2); code(err) != "ownership_conflict" || !strings.Contains(err.Error(), "kubectl-edit") {
		t.Fatalf("edit conflict: %v", err)
	}

	// teardown: everything but PVCs goes, the namespace stays
	out, err := d.Teardown(ctx, v2.Stack)
	if err != nil || !strings.Contains(out, "kept PersistentVolumeClaim/data") {
		t.Fatalf("%q %v", out, err)
	}
	for _, k := range [][2]string{{"ConfigMap", "cfg"}, {"Deployment", "web"}} {
		if _, err := get(dyn, k[0], k[1]); err == nil {
			t.Errorf("%s/%s survived teardown", k[0], k[1])
		}
	}
	if _, err := get(dyn, "PersistentVolumeClaim", "data"); err != nil {
		t.Error("PVC deleted by teardown")
	}
	if _, err := get(dyn, "ConfigMap", "foreign"); err != nil {
		t.Error("foreign object deleted")
	}
	if _, err := dyn.Resource(nsGVR).Get(ctx, kindNS, metav1.GetOptions{}); err != nil {
		t.Error("namespace deleted")
	}
}
