// Package inventory turns informer caches into service rows, drift and stack health (K8S-061..062, 065).
package inventory

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

// Input is everything Compute needs; it never touches a cluster (K8S-061).
type Input struct {
	Inventory                        []k8s.Ref        // desired revision inventory (K8S-026)
	Rendered                         []map[string]any // desired revision rendered objects
	Revision                         int
	Databases                        []string // revision database_services (Kind/name)
	Frozen, Unreachable, Progressing bool
	Cached                           []map[string]any // cached workloads and pods of the stack
}

type Container struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// Service is the Service shape of 03.
type Service struct {
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	State      string      `json:"state"`
	Health     string      `json:"health"`
	Desired    int         `json:"desired_replicas"`
	Ready      int         `json:"ready_replicas"`
	ImageRef   *string     `json:"image_ref"`
	ImageID    *string     `json:"image_id"`
	IsDatabase bool        `json:"is_database"`
	Containers []Container `json:"containers"`
}

// Drift is the DriftItem shape of 03 (04 names, `service` = Kind/name).
type Drift struct {
	Kind     string  `json:"kind"`
	Service  string  `json:"service"`
	Expected *string `json:"expected"`
	Actual   *string `json:"actual"`
	Message  string  `json:"message"`
}

type Result struct {
	Services []Service
	Drift    []Drift
	Health   string
}

func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func num(o any, def int, keys ...string) int {
	switch v := k8s.Dig(o, keys...).(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

func lbl(o map[string]any, k string) string {
	if v := k8s.Dig(o, "metadata", "labels", k); v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// noDigest drops "@sha256:..." so drift compares repo:tag only (K8S-062).
func noDigest(img string) string { s, _, _ := strings.Cut(img, "@"); return s }

func tmplContainers(o map[string]any) (out []Container) {
	l, _ := k8s.Dig(k8s.PodSpec(o), "containers").([]any)
	for _, c := range l {
		m, _ := c.(map[string]any)
		n, _ := m["name"].(string)
		i, _ := m["image"].(string)
		out = append(out, Container{n, i})
	}
	return
}

// replicas returns desired and ready counts per kind.
func replicas(o map[string]any) (desired, ready int) {
	switch k8s.Kind(o) {
	case "Deployment", "StatefulSet":
		return num(o, 1, "spec", "replicas"), num(o, 0, "status", "readyReplicas")
	case "DaemonSet":
		return num(o, 0, "status", "desiredNumberScheduled"), num(o, 0, "status", "numberReady")
	case "Job":
		return num(o, 1, "spec", "completions"), num(o, 0, "status", "succeeded")
	}
	return 0, 0 // CronJob
}

func podReady(p map[string]any) bool {
	cs, _ := k8s.Dig(p, "status", "conditions").([]any)
	for _, c := range cs {
		if m, _ := c.(map[string]any); m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// imageID is the imageID of the first container of a ready pod selected by the workload.
func imageID(w map[string]any, pods []map[string]any) string {
	sel, _ := k8s.Dig(w, "spec", "selector", "matchLabels").(map[string]any)
	if len(sel) == 0 {
		return ""
	}
	for _, p := range pods {
		match := true
		for k, v := range sel {
			if lbl(p, k) != v {
				match = false
			}
		}
		if cs, _ := k8s.Dig(p, "status", "containerStatuses").([]any); match && podReady(p) && len(cs) > 0 {
			id, _ := cs[0].(map[string]any)["imageID"].(string)
			return id
		}
	}
	return ""
}

// Compute is pure: services, drift and health of one stack from cached objects (K8S-062, 03 health precedence).
func Compute(in Input) Result {
	want := map[string]bool{} // inventory workloads
	for _, r := range in.Inventory {
		if k8s.IsWorkload(r.Kind) {
			want[r.Kind+"/"+r.Name] = true
		}
	}
	rendered := map[string]map[string]any{}
	for _, o := range in.Rendered {
		rendered[k8s.Name(o)] = o
	}
	have, pods := map[string]map[string]any{}, []map[string]any{}
	for _, o := range in.Cached {
		if k8s.Kind(o) == "Pod" {
			pods = append(pods, o)
		} else {
			have[k8s.Name(o)] = o
		}
	}
	names := []string{}
	for n := range want {
		names = append(names, n)
	}
	for n := range have {
		if !want[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	r := Result{Services: []Service{}, Drift: []Drift{}}
	rev := strconv.Itoa(in.Revision)
	notReady := false
	for _, n := range names {
		kind, _, _ := strings.Cut(n, "/")
		o := have[n]
		s := Service{Name: n, Kind: kind, IsDatabase: slices.Contains(in.Databases, n), Containers: []Container{}}
		if o == nil {
			s.State, s.Health = "missing", "none"
			r.Drift = append(r.Drift, Drift{"service_missing", n, nil, nil, n + " is in the revision but not in the cluster"})
			r.Services = append(r.Services, s)
			continue
		}
		s.Desired, s.Ready = replicas(o)
		s.Containers = tmplContainers(o)
		if len(s.Containers) > 0 {
			s.ImageRef = ptr(s.Containers[0].Image)
		}
		s.ImageID = ptr(imageID(o, pods))
		s.State, s.Health = "running", "healthy"
		if s.Ready < s.Desired {
			notReady = true
			s.State, s.Health = "degraded", "unhealthy"
			if num(o, 0, "status", "observedGeneration") < num(o, 0, "metadata", "generation") ||
				(kind != "DaemonSet" && num(o, s.Desired, "status", "updatedReplicas") < s.Desired) {
				s.State, s.Health = "progressing", "starting"
			}
		}
		r.Services = append(r.Services, s)
		if !want[n] {
			r.Drift = append(r.Drift, Drift{"service_extra", n, nil, nil, n + " is labelled for this stack but not in the revision"})
			continue
		}
		for _, c := range s.Containers {
			for _, rc := range tmplContainers(rendered[n]) {
				if rc.Name == c.Name && noDigest(rc.Image) != noDigest(c.Image) {
					r.Drift = append(r.Drift, Drift{"image_mismatch", n, ptr(rc.Image), ptr(c.Image), n + " container " + c.Name + " runs a different image"})
				}
			}
		}
		if in.Revision > 0 && lbl(o, "control-8/revision") != rev {
			r.Drift = append(r.Drift, Drift{"label_mismatch", n, ptr(rev), ptr(lbl(o, "control-8/revision")), n + " control-8/revision label is not the desired revision"})
		}
	}
	switch { // precedence: unknown > frozen > progressing > degraded > drifted > healthy
	case in.Unreachable:
		r.Health = "unknown"
	case in.Frozen:
		r.Health = "frozen"
	case in.Progressing:
		r.Health = "progressing"
	case notReady:
		r.Health = "degraded"
	case len(r.Drift) > 0:
		r.Health = "drifted"
	default:
		r.Health = "healthy"
	}
	return r
}
