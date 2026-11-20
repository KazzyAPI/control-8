package api

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/version"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/targets"
)

type okV struct{ err error }

func (o *okV) ServerVersion() (*version.Info, error) {
	return &version.Info{GitVersion: "v1.31.0"}, o.err
}

// D-49 read-only /targets, K8S-003 target.status, R-8 delivery to a viewer and not to a roleless user.
func TestK8S003_TargetsAPIAndEvent(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.login()
	e.s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES('r1','va','a','viewer')`)
	v := &okV{}
	tc := targets.NewChecker([]targets.Spec{{ID: "tgt_1", Name: "home", Kind: "kubernetes", Tier: "non-prod", KubeAuth: "kubeconfig", KubeconfigEnv: "C8_KUBECONFIG_HOME"}},
		func(targets.Spec) (targets.Versioner, error) { return v, nil })
	tc.Publish = e.s.publishTarget
	e.s.targets = tc
	sub, _, _ := e.s.bus.Subscribe(nil)

	tc.Check("home")
	ev := <-sub.C
	if ev.Type != "target.status" || !e.s.visible(&auth.Principal{ID: "u1", Groups: []string{"va"}}, ev) ||
		e.s.visible(&auth.Principal{ID: "u2"}, ev) {
		t.Fatalf("event %+v", ev)
	}

	w := e.do("GET", "/api/v1/targets", "", admin)
	var l struct{ Items []map[string]any }
	json.Unmarshal(w.Body.Bytes(), &l)
	if w.Code != 200 || len(l.Items) != 1 || l.Items[0]["reachable"] != true || l.Items[0]["kubeconfig_env"] != "C8_KUBECONFIG_HOME" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/v1/targets/home", "", admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"server_version":"v1.31.0"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/v1/targets/nope", "", admin); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := e.do("POST", "/api/v1/targets", `{}`, admin); w.Code == 200 || w.Code == 201 { // no write endpoint
		t.Fatal(w.Code)
	}
	if w := e.do("GET", "/api/v1/targets", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := e.do("GET", "/api/v1/targets", "", e.user("nobody", `[]`)); w.Code != 403 {
		t.Fatalf("roleless = %d", w.Code)
	}
}
