package kubernetes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

func fakeDyn() *dynfake.FakeDynamicClient {
	lk := map[schema.GroupVersionResource]string{}
	for _, g := range watchedGVRs {
		lk[g] = map[string]string{"pods": "PodList"}[g.Resource]
		if lk[g] == "" {
			for k, v := range kinds {
				if v.resource == g.Resource {
					lk[g] = k + "List"
				}
			}
		}
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lk)
}

var depGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

func dep(replicas, rv string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "shop", "resourceVersion": rv,
			"labels": map[string]any{"control-8/managed": "true", "control-8/stack": "stk_1"}},
		"spec": map[string]any{"replicas": replicas}}}
}

type sink struct {
	mu  sync.Mutex
	ids []string
	ch  chan struct{}
}

func newSink() *sink { return &sink{ch: make(chan struct{}, 100)} }
func (s *sink) enqueue(id string) {
	s.mu.Lock()
	s.ids = append(s.ids, id)
	s.mu.Unlock()
	s.ch <- struct{}{}
}
func (s *sink) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("no enqueue within 3s")
	}
}
func (s *sink) drain() {
	for len(s.ch) > 0 {
		<-s.ch
	}
}

func lists(f *dynfake.FakeDynamicClient) (n int) {
	for _, a := range f.Actions() {
		if a.GetVerb() == "list" {
			n++
		}
	}
	return
}

// K8S-060, K8S-061: an update arrives through the informer watch, enqueues the stack id, and costs no API list.
func TestK8S061_InformerEnqueuesWithoutListing(t *testing.T) {
	f := fakeDyn()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := f.Resource(depGVR).Namespace("shop")
	if _, err := res.Create(ctx, dep("1", "1"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	sk := newSink()
	c, err := NewDriver(f).Watch(ctx, rt.WatchOptions{Resync: time.Hour, Poll: time.Hour, Enqueue: sk.enqueue})
	if err != nil {
		t.Fatal(err)
	}
	if c.Sync() != "watching" || len(c.Objects("stk_1")) != 1 || len(c.Objects("other")) != 0 {
		t.Fatalf("sync=%s objs=%d", c.Sync(), len(c.Objects("stk_1")))
	}
	sk.drain()
	before := lists(f)
	for i := 2; ; i++ { // the fake watch may attach just after the first list: retry until an event lands
		res.Update(ctx, dep(string(rune('0'+i)), string(rune('0'+i))), metav1.UpdateOptions{})
		select {
		case <-sk.ch:
		case <-time.After(300 * time.Millisecond):
			if i > 8 {
				t.Fatal("no enqueue")
			}
			continue
		}
		break
	}
	if sk.ids[len(sk.ids)-1] != "stk_1" || lists(f) != before {
		t.Fatalf("ids=%v lists %d -> %d", sk.ids, before, lists(f))
	}
}

// K8S-064: watch forbidden by RBAC -> list-poll fallback, sync "polling", changes and deletes still enqueue.
func TestK8S064_PollFallbackWhenWatchForbidden(t *testing.T) {
	f := fakeDyn()
	f.PrependWatchReactor("*", func(a ktesting.Action) (bool, watch.Interface, error) {
		return true, nil, apierrors.NewForbidden(a.GetResource().GroupResource(), "", errors.New("watch not allowed"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := f.Resource(depGVR).Namespace("shop")
	res.Create(ctx, dep("1", "1"), metav1.CreateOptions{})
	sk := newSink()
	c, err := NewDriver(f).Watch(ctx, rt.WatchOptions{Resync: time.Hour, Poll: 20 * time.Millisecond, Enqueue: sk.enqueue})
	if err != nil || c.Sync() != "polling" {
		t.Fatalf("%v %v", err, c)
	}
	sk.wait(t) // first poll sees the object
	sk.drain()
	res.Update(ctx, dep("3", "2"), metav1.UpdateOptions{})
	sk.wait(t)
	sk.drain()
	res.Delete(ctx, "web", metav1.DeleteOptions{})
	sk.wait(t)
	deadline := time.Now().Add(time.Second)
	for len(c.Objects("stk_1")) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(c.Objects("stk_1")) != 0 {
		t.Fatal("deleted object still cached")
	}
}
