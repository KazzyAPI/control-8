package kubernetes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

type healthWant struct {
	Healthy, Failed                    bool
	Reason                             string
	Desired, Updated, Ready, Available int
}

type healthCase struct {
	Name         string
	Rev          int
	Now, Since   time.Time
	Plan, Cached []map[string]any
	Want         healthWant
}

// K8S-063 (rollout health criteria in 08): pure function over testdata/health/*.yaml fixtures.
func TestK8S063_HealthFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/health/*.yaml")
	if len(files) < 4 {
		t.Fatalf("fixtures: %v", files)
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var doc struct{ Cases []healthCase }
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, c := range doc.Cases {
			t.Run(filepath.Base(f)+"/"+c.Name, func(t *testing.T) {
				r := Health(c.Plan, c.Cached, c.Rev, c.Since, c.Now)
				if r.Healthy != c.Want.Healthy || (r.Failed != nil) != c.Want.Failed {
					t.Fatalf("healthy=%v failed=%v: %+v", r.Healthy, r.Failed, r.Workloads)
				}
				w := r.Workloads[0]
				if !strings.Contains(w.Reason, c.Want.Reason) && c.Want.Reason != "" {
					t.Fatalf("reason %q want %q", w.Reason, c.Want.Reason)
				}
				for _, p := range [][2]int{{w.Desired, c.Want.Desired}, {w.Updated, c.Want.Updated}, {w.Ready, c.Want.Ready}, {w.Available, c.Want.Available}} {
					if p[1] != 0 && p[0] != p[1] {
						t.Fatalf("counts %+v want %+v", w, c.Want)
					}
				}
			})
		}
	}
}

// 08: kinds other than the four workload kinds have nothing to wait for.
func TestK8S063_OtherKindsHealthy(t *testing.T) {
	r := Health([]map[string]any{{"kind": "ConfigMap", "metadata": map[string]any{"name": "c"}}}, nil, 1, time.Time{}, time.Time{})
	if !r.Healthy || len(r.Workloads) != 0 {
		t.Fatalf("%+v", r)
	}
}
