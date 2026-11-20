//go:build k8s

package targets

import (
	"os"
	"testing"
)

// M5 acceptance (needs `make kind-up`): a kind kubeconfig target is reachable after one check.
func TestK8S003_KindReachable(t *testing.T) {
	path := os.Getenv("KUBECONFIG")
	if path == "" {
		t.Skip("KUBECONFIG not set (run scripts/kind-up.sh)")
	}
	c := NewChecker([]Spec{{ID: "tgt_k", Name: "kind", Kind: "kubernetes", Tier: "non-prod", KubeAuth: "kubeconfig", KubeconfigPath: path}},
		BuildClient(os.Getenv, os.ReadFile))
	var evs []Target
	c.Publish = func(t Target) { evs = append(evs, t) }
	c.Check("kind")
	tg, _ := c.Get("kind")
	if !tg.Reachable || tg.ServerVersion == nil || len(evs) != 1 {
		t.Fatalf("%+v events=%d", tg, len(evs))
	}
}
