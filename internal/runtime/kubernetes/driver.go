package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

// FieldManager is the only field manager Control 8 writes with (K8S-000).
const FieldManager = "control-8"

// Applier is the one server-side-apply call, isolated so tests can record it (the fake dynamic
// client has no real SSA). Implementations must never force (K8S-043).
type Applier interface {
	Apply(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dryRun bool) error
}

type dynApplier struct{ dyn dynamic.Interface }

func (a dynApplier) Apply(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dryRun bool) error {
	force := false
	opts := metav1.PatchOptions{FieldManager: FieldManager, Force: &force}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	_, err := a.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, types.ApplyPatchType, data, opts)
	return err
}

// Driver is the Kubernetes runtime.Driver (08 Apply section). All cluster writes in Control 8 live here.
type Driver struct {
	dyn dynamic.Interface
	ssa Applier
}

func NewDriver(dyn dynamic.Interface) *Driver { return &Driver{dyn: dyn, ssa: dynApplier{dyn}} }

var _ rt.Driver = (*Driver)(nil)

// kinds maps each K8S-021 kind to its resource and the apiVersion used to list and delete it.
var kinds = map[string]struct{ resource, apiVersion string }{
	"ServiceAccount": {"serviceaccounts", "v1"}, "ConfigMap": {"configmaps", "v1"}, "PersistentVolumeClaim": {"persistentvolumeclaims", "v1"},
	"Service": {"services", "v1"}, "NetworkPolicy": {"networkpolicies", "networking.k8s.io/v1"},
	"Deployment": {"deployments", "apps/v1"}, "StatefulSet": {"statefulsets", "apps/v1"}, "DaemonSet": {"daemonsets", "apps/v1"},
	"Job": {"jobs", "batch/v1"}, "CronJob": {"cronjobs", "batch/v1"},
	"HorizontalPodAutoscaler": {"horizontalpodautoscalers", "autoscaling/v2"}, "PodDisruptionBudget": {"poddisruptionbudgets", "policy/v1"},
	"Ingress": {"ingresses", "networking.k8s.io/v1"},
}

// applyOrder is the 08 kind order.
var applyOrder = []string{"ServiceAccount", "ConfigMap", "PersistentVolumeClaim", "Service", "NetworkPolicy",
	"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "HorizontalPodAutoscaler", "PodDisruptionBudget", "Ingress"}

var nsGVR = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}

func gvrFor(kind, apiVersion string) (schema.GroupVersionResource, error) {
	k, ok := kinds[kind]
	if !ok {
		return schema.GroupVersionResource{}, &rt.Error{Code: "invalid_manifests", Detail: "kind " + kind + " is not allowed"}
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, &rt.Error{Code: "invalid_manifests", Detail: "bad apiVersion " + apiVersion}
	}
	return gv.WithResource(k.resource), nil
}

func gvrOf(o map[string]any) (schema.GroupVersionResource, error) {
	return gvrFor(Kind(o), str(o, "apiVersion"))
}

// classify maps a client error to the stable codes of 08 "Failure modes".
func classify(err error, verb, what string) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsConflict(err): // SSA field conflict: the message names the conflicting managers (K8S-043)
		return &rt.Error{Code: "ownership_conflict", Detail: what + ": " + err.Error()}
	case apierrors.IsForbidden(err):
		return &rt.Error{Code: "forbidden_by_rbac", Detail: verb + " " + what}
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		return &rt.Error{Code: "invalid_manifests", Detail: what + ": " + err.Error()}
	}
	return &rt.Error{Code: verb + "_failed", Detail: what + ": " + err.Error()}
}

func isArgo(o *unstructured.Unstructured) bool {
	_, inst := o.GetLabels()["argocd.argoproj.io/instance"]
	_, track := o.GetAnnotations()["argocd.argoproj.io/tracking-id"]
	return inst || track
}

// Precheck is read-only: it GETs every object (K8S-041/042) and only then dry-runs the apply of each
// (K8S-043 validation). Any problem refuses the whole plan before a single write.
func (d *Driver) Precheck(ctx context.Context, p rt.Plan) error {
	for _, o := range p.Objects {
		gvr, err := gvrOf(o)
		if err != nil {
			return err
		}
		name := Name(o)
		cur, err := d.dyn.Resource(gvr).Namespace(p.Stack.Namespace).Get(ctx, str(o, "metadata", "name"), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			continue
		case err != nil:
			return classify(err, "get", name)
		case isArgo(cur): // even when it also carries our labels (K8S-042)
			return &rt.Error{Code: "argo_managed", Detail: name + " is managed by Argo CD"}
		case cur.GetLabels()["control-8/stack"] != p.Stack.ID:
			return &rt.Error{Code: "ownership_conflict", Detail: name + " exists and is not owned by this stack"}
		}
	}
	// A missing namespace means no dry-run is possible without writing; Apply dry-runs after creating it.
	if _, err := d.dyn.Resource(nsGVR).Get(ctx, p.Stack.Namespace, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return classify(err, "get", "Namespace/"+p.Stack.Namespace)
	}
	return d.each(ctx, p, true)
}

// each server-side applies (or dry-runs) every object in kind order, never forcing.
func (d *Driver) each(ctx context.Context, p rt.Plan, dry bool) error {
	for _, o := range ordered(p.Objects) {
		gvr, err := gvrOf(o)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(o)
		if err := d.ssa.Apply(ctx, gvr, p.Stack.Namespace, str(o, "metadata", "name"), data, dry); err != nil {
			return classify(err, "apply", Name(o))
		}
	}
	return nil
}

func ordered(objs []map[string]any) []map[string]any {
	out := slices.Clone(objs)
	slices.SortStableFunc(out, func(a, b map[string]any) int {
		return slices.Index(applyOrder, Kind(a)) - slices.Index(applyOrder, Kind(b))
	})
	return out
}

// ensureNamespace creates the Kubernetes namespace if missing (K8S-011); an existing one is used as-is.
func (d *Driver) ensureNamespace(ctx context.Context, name string) (created bool, err error) {
	_, err = d.dyn.Resource(nsGVR).Get(ctx, name, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		return false, classify(err, "get", "Namespace/"+name)
	}
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": name,
		"labels": map[string]any{"app.kubernetes.io/managed-by": "control-8", "control-8/namespace": name}}}}
	_, err = d.dyn.Resource(nsGVR).Create(ctx, ns, metav1.CreateOptions{FieldManager: FieldManager})
	if apierrors.IsAlreadyExists(err) {
		return false, nil
	}
	return err == nil, classify(err, "create", "Namespace/"+name)
}

func (d *Driver) Apply(ctx context.Context, p rt.Plan) (string, error) {
	created, err := d.ensureNamespace(ctx, p.Stack.Namespace)
	if err != nil {
		return "", err
	}
	if created {
		if err := d.each(ctx, p, true); err != nil {
			return "namespace " + p.Stack.Namespace + " created\n", err
		}
	}
	var out strings.Builder
	if created {
		fmt.Fprintf(&out, "namespace %s created\n", p.Stack.Namespace)
	}
	for _, o := range ordered(p.Objects) {
		if err := d.each(ctx, rt.Plan{Stack: p.Stack, Objects: []map[string]any{o}}, false); err != nil {
			return out.String(), err
		}
		fmt.Fprintf(&out, "applied %s\n", Name(o))
	}
	return out.String(), nil
}

func (d *Driver) Prune(ctx context.Context, p rt.Plan) (string, error) {
	keep := map[string]bool{}
	for _, o := range p.Objects {
		keep[Name(o)] = true
	}
	return d.sweep(ctx, p.Stack, keep)
}

func (d *Driver) Teardown(ctx context.Context, s rt.Stack) (string, error) {
	return d.sweep(ctx, s, nil)
}

// sweep lists the stack's labelled objects of every K8S-021 kind and deletes those not in keep
// (background propagation). PVCs are only reported, never deleted (D-37); Argo-owned objects are skipped (K8S-042).
func (d *Driver) sweep(ctx context.Context, s rt.Stack, keep map[string]bool) (string, error) {
	var out strings.Builder
	for _, kind := range applyOrder {
		gv, _ := schema.ParseGroupVersion(kinds[kind].apiVersion)
		res := d.dyn.Resource(gv.WithResource(kinds[kind].resource)).Namespace(s.Namespace)
		list, err := res.List(ctx, metav1.ListOptions{LabelSelector: "control-8/stack=" + s.ID})
		if apierrors.IsNotFound(err) { // kind not served by this cluster
			continue
		} else if err != nil {
			return out.String(), classify(err, "list", kind)
		}
		for i := range list.Items {
			o := &list.Items[i]
			name := kind + "/" + o.GetName()
			switch {
			case keep[name] || isArgo(o):
			case kind == "PersistentVolumeClaim":
				fmt.Fprintf(&out, "kept %s\n", name)
			default:
				bg := metav1.DeletePropagationBackground
				if err := res.Delete(ctx, o.GetName(), metav1.DeleteOptions{PropagationPolicy: &bg}); err != nil && !apierrors.IsNotFound(err) {
					return out.String(), classify(err, "delete", name)
				}
				fmt.Fprintf(&out, "deleted %s\n", name)
			}
		}
	}
	return out.String(), nil
}
