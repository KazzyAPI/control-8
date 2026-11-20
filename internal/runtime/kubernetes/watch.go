package kubernetes

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

const managedSelector = "control-8/managed=true"

// watchedGVRs: the K8S-021 workload kinds plus Pod (K8S-060).
var watchedGVRs = func() (out []schema.GroupVersionResource) {
	for k := range workloads {
		gv, _ := schema.ParseGroupVersion(kinds[k].apiVersion)
		out = append(out, gv.WithResource(kinds[k].resource))
	}
	return append(out, schema.GroupVersionResource{Version: "v1", Resource: "pods"})
}()

type stores struct {
	l       []cache.Store
	polling atomic.Bool
}

func (c *stores) Objects(stackID string) (out []map[string]any) {
	for _, s := range c.l {
		for _, o := range s.List() {
			if u, ok := o.(*unstructured.Unstructured); ok && u.GetLabels()["control-8/stack"] == stackID {
				out = append(out, u.Object)
			}
		}
	}
	return
}

func (c *stores) Sync() string {
	if c.polling.Load() {
		return "polling"
	}
	return "watching"
}

// canWatch probes the watch verb; only "not permitted" selects the polling fallback (K8S-064).
func canWatch(ctx context.Context, r dynamic.NamespaceableResourceInterface) bool {
	one := int64(1)
	w, err := r.Watch(ctx, metav1.ListOptions{LabelSelector: managedSelector, TimeoutSeconds: &one})
	if err != nil {
		return !apierrors.IsForbidden(err) && !apierrors.IsMethodNotSupported(err)
	}
	w.Stop()
	return true
}

// poll lists the kind every `every` into st and enqueues the stacks whose objects changed or vanished.
func poll(ctx context.Context, r dynamic.NamespaceableResourceInterface, st cache.Store, every time.Duration, enq func(any)) {
	for {
		if l, err := r.List(ctx, metav1.ListOptions{LabelSelector: managedSelector}); err == nil {
			seen := map[string]bool{}
			for i := range l.Items {
				u := &l.Items[i]
				k, _ := cache.MetaNamespaceKeyFunc(u)
				seen[k] = true
				if old, ok, _ := st.GetByKey(k); !ok || old.(*unstructured.Unstructured).GetResourceVersion() != u.GetResourceVersion() {
					enq(u)
				}
				st.Update(u)
			}
			for _, o := range st.List() {
				if k, _ := cache.MetaNamespaceKeyFunc(o); !seen[k] {
					st.Delete(o)
					enq(o)
				}
			}
		} else {
			slog.Warn("informer_degraded", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Watch starts one shared dynamic informer factory (label control-8/managed=true, cluster-wide) and returns
// once the caches synced or 10 s passed. A kind whose watch is forbidden is list-polled instead.
func (d *Driver) Watch(ctx context.Context, o rt.WatchOptions) (rt.Cache, error) {
	enq := func(obj any) {
		if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = t.Obj
		}
		if u, ok := obj.(*unstructured.Unstructured); ok && u.GetLabels()["control-8/stack"] != "" {
			o.Enqueue(u.GetLabels()["control-8/stack"])
		}
	}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(d.dyn, o.Resync, metav1.NamespaceAll,
		func(l *metav1.ListOptions) { l.LabelSelector = managedSelector })
	c := &stores{}
	var synced []cache.InformerSynced
	for _, g := range watchedGVRs {
		if !canWatch(ctx, d.dyn.Resource(g)) {
			slog.Warn("informer_degraded", "resource", g.Resource, "reason", "watch not permitted")
			st := cache.NewStore(cache.MetaNamespaceKeyFunc)
			c.polling.Store(true)
			c.l = append(c.l, st)
			go poll(ctx, d.dyn.Resource(g), st, o.Poll, enq)
			continue
		}
		inf := factory.ForResource(g).Informer()
		inf.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: enq, DeleteFunc: enq, UpdateFunc: func(_, n any) { enq(n) }})
		c.l = append(c.l, inf.GetStore())
		synced = append(synced, inf.HasSynced)
	}
	factory.Start(ctx.Done())
	go func() { <-ctx.Done(); factory.Shutdown() }()
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cache.WaitForCacheSync(sctx.Done(), synced...)
	return c, nil
}
