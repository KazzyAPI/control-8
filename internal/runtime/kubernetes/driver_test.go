package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

type call struct {
	gvr      schema.GroupVersionResource
	ns, name string
	dry      bool
}

// recorder stands in for server-side apply (the dynamic fake has none); err, if set, is returned for a name.
type recorder struct {
	mu    sync.Mutex
	calls []call
	fail  map[string]error
}

func (r *recorder) Apply(_ context.Context, gvr schema.GroupVersionResource, ns, name string, _ []byte, dry bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{gvr, ns, name, dry})
	return r.fail[name]
}

func (r *recorder) writes() (n int) {
	for _, c := range r.calls {
		if !c.dry {
			n++
		}
	}
	return
}

func obj(apiVersion, kind, name string, labels map[string]any) *unstructured.Unstructured {
	md := map[string]any{"name": name, "namespace": "shop"}
	if labels != nil {
		md["labels"] = labels
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": md}}
}

func owned(apiVersion, kind, name string) *unstructured.Unstructured {
	return obj(apiVersion, kind, name, map[string]any{"control-8/stack": "stk_1"})
}

func newDriver(t *testing.T, objs ...runtime.Object) (*Driver, *recorder, *dynfake.FakeDynamicClient) {
	t.Helper()
	listKinds := map[schema.GroupVersionResource]string{nsGVR: "NamespaceList"}
	for k, v := range kinds {
		gv, _ := schema.ParseGroupVersion(v.apiVersion)
		listKinds[gv.WithResource(v.resource)] = k + "List"
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)
	rec := &recorder{}
	return &Driver{dyn: dyn, ssa: rec}, rec, dyn
}

func ns(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": name}}}
}

func plan(objs ...map[string]any) rt.Plan {
	return rt.Plan{Stack: rt.Stack{ID: "stk_1", Namespace: "shop"}, Objects: objs}
}

func code(err error) string {
	if e, ok := err.(*rt.Error); ok {
		return e.Code
	}
	return "other:" + err.Error()
}

// K8S-041, K8S-000: an unowned object refuses the whole plan before any write; nothing new is created.
func TestK8S041_OwnershipConflictRefusesBeforeWrite(t *testing.T) {
	d, rec, dyn := newDriver(t, ns("shop"), obj("apps/v1", "Deployment", "web", nil))
	p := plan(obj("v1", "ConfigMap", "cfg", nil).Object, obj("apps/v1", "Deployment", "web", nil).Object)
	err := d.Precheck(context.Background(), p)
	if code(err) != "ownership_conflict" || !strings.Contains(err.Error(), "Deployment/web") {
		t.Fatalf("err = %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("dry-run or write happened before ownership was cleared: %v", rec.calls)
	}
	if _, err := dyn.Resource(kindGVR("ConfigMap")).Namespace("shop").Get(context.Background(), "cfg", getOpts); !apierrors.IsNotFound(err) {
		t.Fatal("ConfigMap was created")
	}
}

// K8S-042: Argo tracking is refused even when the object carries our stack label.
func TestK8S042_ArgoManagedRefused(t *testing.T) {
	a := owned("apps/v1", "Deployment", "web")
	a.SetLabels(map[string]string{"control-8/stack": "stk_1", "argocd.argoproj.io/instance": "web"})
	b := owned("v1", "ConfigMap", "cfg")
	b.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "x"})
	for _, o := range []*unstructured.Unstructured{a, b} {
		d, rec, _ := newDriver(t, ns("shop"), o)
		err := d.Precheck(context.Background(), plan(o.Object))
		if code(err) != "argo_managed" || rec.writes() != 0 {
			t.Fatalf("%s: %v writes=%d", o.GetKind(), err, rec.writes())
		}
	}
}

// K8S-041: objects already owned by this stack pass, then every object is dry-run in kind order (08 Apply 1, 3).
func TestK8S041_PrecheckDryRunsInKindOrder(t *testing.T) {
	d, rec, _ := newDriver(t, ns("shop"), owned("v1", "ConfigMap", "cfg"))
	p := plan(obj("apps/v1", "Deployment", "web", nil).Object, obj("v1", "ConfigMap", "cfg", nil).Object, obj("v1", "ServiceAccount", "sa", nil).Object)
	if err := d.Precheck(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range rec.calls {
		if !c.dry {
			t.Fatal("precheck wrote")
		}
		got = append(got, c.name)
	}
	if strings.Join(got, ",") != "sa,cfg,web" {
		t.Fatalf("order %v", got)
	}
}

// K8S-011: a missing namespace is created on apply (labelled), an existing one is untouched; precheck writes nothing.
func TestK8S011_NamespaceEnsured(t *testing.T) {
	d, rec, dyn := newDriver(t)
	p := plan(obj("v1", "ConfigMap", "cfg", nil).Object)
	if err := d.Precheck(context.Background(), p); err != nil || len(rec.calls) != 0 {
		t.Fatalf("precheck %v calls=%v", err, rec.calls)
	}
	if _, err := dyn.Resource(nsGVR).Get(context.Background(), "shop", getOpts); !apierrors.IsNotFound(err) {
		t.Fatal("precheck created the namespace")
	}
	out, err := d.Apply(context.Background(), p)
	if err != nil || !strings.Contains(out, "namespace shop created") || !strings.Contains(out, "applied ConfigMap/cfg") {
		t.Fatalf("%q %v", out, err)
	}
	n, _ := dyn.Resource(nsGVR).Get(context.Background(), "shop", getOpts)
	if n.GetLabels()["app.kubernetes.io/managed-by"] != "control-8" || n.GetLabels()["control-8/namespace"] != "shop" {
		t.Fatalf("labels %v", n.GetLabels())
	}
	if rec.calls[0].dry != true || rec.calls[len(rec.calls)-1].dry { // created ns => dry-run first, then the real apply
		t.Fatalf("calls %v", rec.calls)
	}
}

// K8S-043: field-manager conflicts and RBAC denials surface as stable codes; the plan stops at the failure.
func TestK8S043_SSAConflictAndRBAC(t *testing.T) {
	d, rec, _ := newDriver(t, ns("shop"))
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "deployments"}, "web", nil)
	conflict.ErrStatus.Message = `Apply failed with 1 conflict: conflict with "kubectl-edit": .spec.replicas`
	rec.fail = map[string]error{"web": conflict}
	p := plan(obj("v1", "ConfigMap", "cfg", nil).Object, obj("apps/v1", "Deployment", "web", nil).Object, obj("v1", "Service", "svc", nil).Object)
	_, err := d.Apply(context.Background(), p)
	if code(err) != "ownership_conflict" || !strings.Contains(err.Error(), "kubectl-edit") {
		t.Fatalf("err = %v", err)
	}
	if last := rec.calls[len(rec.calls)-1]; last.name != "web" {
		t.Fatalf("applied past the conflict: %v", rec.calls)
	}
	if got := code(classify(apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "cfg", nil), "apply", "ConfigMap/cfg")); got != "forbidden_by_rbac" {
		t.Fatal(got)
	}
}

// D-37, K8S-000, D-31: prune deletes only this stack's labelled objects missing from the inventory, never PVCs.
func TestD37_PruneSelectionKeepsPVCAndOthers(t *testing.T) {
	d, _, dyn := newDriver(t, ns("shop"),
		owned("v1", "ConfigMap", "old"), owned("v1", "ConfigMap", "keep"), owned("v1", "PersistentVolumeClaim", "data"),
		obj("v1", "ConfigMap", "foreign", nil),                                            // unlabelled
		obj("v1", "ConfigMap", "other-stack", map[string]any{"control-8/stack": "stk_2"}), // another stack's
		argoOwned(owned("apps/v1", "Deployment", "argo-dep")))
	out, err := d.Prune(context.Background(), plan(obj("v1", "ConfigMap", "keep", nil).Object))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deleted ConfigMap/old") || !strings.Contains(out, "kept PersistentVolumeClaim/data") || strings.Count(out, "deleted") != 1 {
		t.Fatalf("out = %q", out)
	}
	for _, c := range []struct {
		kind, name string
		want       bool
	}{{"ConfigMap", "old", false}, {"ConfigMap", "keep", true}, {"ConfigMap", "foreign", true}, {"ConfigMap", "other-stack", true},
		{"PersistentVolumeClaim", "data", true}, {"Deployment", "argo-dep", true}} {
		_, err := dyn.Resource(kindGVR(c.kind)).Namespace("shop").Get(context.Background(), c.name, getOpts)
		if (err == nil) != c.want {
			t.Errorf("%s/%s exists=%v want %v", c.kind, c.name, err == nil, c.want)
		}
	}
}

// D-37, D-35: teardown deletes all labelled objects except PVCs and never the namespace.
func TestD37_TeardownKeepsPVCAndNamespace(t *testing.T) {
	d, _, dyn := newDriver(t, ns("shop"), owned("apps/v1", "Deployment", "web"), owned("v1", "Service", "svc"),
		owned("v1", "PersistentVolumeClaim", "data"), obj("v1", "ConfigMap", "foreign", nil))
	out, err := d.Teardown(context.Background(), rt.Stack{ID: "stk_1", Namespace: "shop"})
	if err != nil || !strings.Contains(out, "kept PersistentVolumeClaim/data") {
		t.Fatalf("%q %v", out, err)
	}
	for kind, name := range map[string]string{"Deployment": "web", "Service": "svc"} {
		if _, err := dyn.Resource(kindGVR(kind)).Namespace("shop").Get(context.Background(), name, getOpts); !apierrors.IsNotFound(err) {
			t.Errorf("%s/%s survived", kind, name)
		}
	}
	for kind, name := range map[string]string{"PersistentVolumeClaim": "data", "ConfigMap": "foreign"} {
		if _, err := dyn.Resource(kindGVR(kind)).Namespace("shop").Get(context.Background(), name, getOpts); err != nil {
			t.Errorf("%s/%s deleted", kind, name)
		}
	}
	if _, err := dyn.Resource(nsGVR).Get(context.Background(), "shop", getOpts); err != nil {
		t.Fatal("namespace deleted")
	}
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "delete" && (a.GetResource().Resource == "namespaces" || a.GetResource().Resource == "persistentvolumeclaims") {
			t.Fatalf("forbidden delete %v", a)
		}
	}
}

func argoOwned(o *unstructured.Unstructured) *unstructured.Unstructured {
	o.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "x"})
	return o
}

// K8S-043, D-31: the real applier PATCHes with application/apply-patch+yaml, fieldManager=control-8 and force=false
// (the dynamic fake drops patch options, so this runs against an httptest API server).
func TestK8S043_ApplierNeverForces(t *testing.T) {
	var method, ctype, query, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, ctype, query, path = r.Method, r.Header.Get("Content-Type"), r.URL.RawQuery, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cfg"}}`))
	}))
	defer srv.Close()
	dyn, err := dynamic.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	if err := NewDriver(dyn).ssa.Apply(context.Background(), gvr, "shop", "cfg", []byte(`{"kind":"ConfigMap"}`), true); err != nil {
		t.Fatal(err)
	}
	if method != "PATCH" || ctype != "application/apply-patch+yaml" || path != "/api/v1/namespaces/shop/configmaps/cfg" ||
		!strings.Contains(query, "fieldManager=control-8") || !strings.Contains(query, "force=false") || !strings.Contains(query, "dryRun=All") {
		t.Fatalf("%s %s %s ? %s", method, ctype, path, query)
	}
}

var getOpts = metav1.GetOptions{}

func kindGVR(kind string) schema.GroupVersionResource {
	gvr, _ := gvrFor(kind, kinds[kind].apiVersion)
	return gvr
}
