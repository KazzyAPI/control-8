package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/inventory"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
)

type cacheStub struct{ objs []map[string]any }

func (c cacheStub) Objects(string) []map[string]any { return c.objs }
func (c cacheStub) Sync() string                    { return "watching" }

// K8S-061, K8S-062, EVT-004, R-8: observed change -> stack.updated with services/drift/health to a viewer only;
// GET services and /inventory serve the rows (inventory filtered by role).
func TestK8S061_ObservedStackUpdatedAndEndpoints(t *testing.T) {
	e, admin, _, viewer, other := m6env(t)
	e.do("POST", "/api/v1/namespaces/a/stacks", `{"slug":"s1","asset":"web","target":"home"}`, admin)
	e.do("POST", "/api/v1/namespaces/b/assets", `{"slug":"x","name":"X"}`, admin)
	var id string
	e.s.db.QueryRow(`SELECT id FROM stack WHERE slug='s1'`).Scan(&id)
	rendered := `[{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web"},"spec":{"template":{"spec":{"containers":[{"name":"web","image":"ghcr.io/acme/web:1"}]}}}}]`
	e.s.db.Exec(`INSERT INTO revision(id,stack_id,number,safety_profile,rendered,inventory,rollout_timeout_seconds,policy_result,created_by,created_at)
		VALUES('rev_1',?,1,'as_is',?,?,300,'{}','u','t')`, id, rendered, `[{"group":"apps","kind":"Deployment","name":"web"}]`)
	e.s.db.Exec(`UPDATE stack SET state='active',desired_revision=1 WHERE id=?`, id)

	eng := inventory.New(e.s.db, time.Millisecond)
	WithInventory(eng)(e.s)
	eng.SetCache("home", cacheStub{}) // the deployment was deleted by hand
	sub, _, _ := e.s.bus.Subscribe(nil)
	eng.Recompute(id)

	var ev events.Event
	for ev.Type != "stack.updated" {
		select {
		case ev = <-sub.C:
		case <-time.After(2 * time.Second):
			t.Fatal("no stack.updated")
		}
	}
	got, _ := json.Marshal(ev.Data)
	if !strings.Contains(string(got), `"service_missing"`) || !strings.Contains(string(got), `"health":"drifted"`) {
		t.Fatalf("%s", got)
	}
	if !e.s.visible(&auth.Principal{ID: "u1", Groups: []string{"va"}, AuthMethod: "oidc"}, ev) ||
		e.s.visible(&auth.Principal{ID: "u2", AuthMethod: "oidc"}, ev) {
		t.Fatal("stack.updated visibility")
	}
	// unchanged recompute publishes nothing
	eng.Recompute(id)
	select {
	case x := <-sub.C:
		t.Fatalf("unexpected %s", x.Type)
	case <-time.After(50 * time.Millisecond):
	}
	if w := e.do("GET", "/api/v1/namespaces/a/stacks/s1/services", "", viewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"Deployment/web"`) || !strings.Contains(w.Body.String(), `"missing"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/v1/namespaces/a/stacks/s1/services", "", other); w.Code != 404 && w.Code != 403 {
		t.Fatalf("outsider %d", w.Code)
	}
	list := func(h map[string]string) string { return e.do("GET", "/api/v1/inventory", "", h).Body.String() }
	if b := list(viewer); !strings.Contains(b, `"slug":"s1"`) || !strings.Contains(b, `"service_missing"`) {
		t.Fatalf("inventory %s", b)
	}
	if b := list(other); strings.Contains(b, "s1") {
		t.Fatalf("outsider inventory %s", b)
	}
}

var _ rt.Cache = cacheStub{}
