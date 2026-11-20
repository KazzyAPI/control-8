package kubernetes

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

var update = flag.Bool("update", false, "rewrite golden files")

func yamlDocs(objs []map[string]any) string {
	var docs []string
	for _, o := range objs {
		b, _ := yaml.Marshal(o)
		docs = append(docs, string(b))
	}
	return strings.Join(docs, "---\n")
}

// K8S-022, K8S-024, K8S-026: golden render (labels on objects and pod templates, never selectors).
func TestK8S024_RenderGolden(t *testing.T) {
	ins, _ := filepath.Glob("testdata/*.in.yaml")
	for _, in := range ins {
		raw, _ := os.ReadFile(in)
		objs, err := Parse(string(raw))
		if err != nil {
			t.Fatal(in, err)
		}
		out, inv := Render(objs, "stk_test", "shop", 3, "managed_rollout")
		got := yamlDocs(out)
		golden := strings.Replace(in, ".in.yaml", ".golden.yaml", 1)
		if *update {
			os.WriteFile(golden, []byte(got), 0o644)
		}
		want, _ := os.ReadFile(golden)
		if got != string(want) {
			t.Errorf("%s differs from %s (run -update)\n%s", in, golden, got)
		}
		if len(inv) != len(out) {
			t.Errorf("inventory %v", inv)
		}
	}
}

// K8S-020: invalid inputs are rejected.
func TestK8S020_ParseErrors(t *testing.T) {
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n"
	for name, in := range map[string]string{
		"empty":     "",
		"empty doc": cm + "---\n",
		"no name":   "apiVersion: v1\nkind: ConfigMap\n",
		"list":      "apiVersion: v1\nkind: List\nmetadata: {name: a}\n",
		"duplicate": cm + "---\n" + cm,
		"not yaml":  "a: [",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if objs, err := Parse(cm + "---\n" + strings.Replace(cm, "ConfigMap", "Secret", 1)); err != nil || len(objs) != 2 {
		t.Fatal(err)
	}
}

func TestDetectDatabases(t *testing.T) {
	objs, _ := Parse(mustRead(t, "testdata/cron.in.yaml"))
	if got := DetectDatabases(objs); len(got) != 1 || got[0] != "StatefulSet/db" {
		t.Fatal(got)
	}
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
