package kubernetes

import (
	"fmt"
	"strings"
	"time"
)

// BadPodAfter is how long a pod may sit in a failing waiting state before the rollout is unhealthy (08).
const BadPodAfter = 60 * time.Second

// WorkloadHealth is one entry of deployment.rollout.workloads (02).
type WorkloadHealth struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Desired   int    `json:"desired"`
	Updated   int    `json:"updated"`
	Ready     int    `json:"ready"`
	Available int    `json:"available"`
	Reason    string `json:"reason"`
	Healthy   bool   `json:"-"`
	Failed    bool   `json:"-"` // immediately unhealthy: no point waiting for the timeout
}

// HealthResult: Healthy only when every workload of the revision passes.
type HealthResult struct {
	Workloads []WorkloadHealth
	Healthy   bool
	Failed    *WorkloadHealth // first immediately-unhealthy workload
}

func n(o any, def int, keys ...string) int {
	switch v := Dig(o, keys...).(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

func cond(o map[string]any, typ string) (status, reason string) {
	cs, _ := Dig(o, "status", "conditions").([]any)
	for _, c := range cs {
		if str(c, "type") == typ {
			return str(c, "status"), str(c, "reason")
		}
	}
	return
}

var badWaiting = map[string]bool{"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true}

// podFailure returns a reason when a pod created at/after since has a container stuck in a failing
// waiting state for longer than BadPodAfter (age measured from the pod's start, D-80).
func podFailure(p map[string]any, since, now time.Time) string {
	ct, _ := time.Parse(time.RFC3339, str(p, "metadata", "creationTimestamp"))
	if ct.Before(since) {
		return ""
	}
	start, err := time.Parse(time.RFC3339, str(p, "status", "startTime"))
	if err != nil {
		start = ct
	}
	if now.Sub(start) <= BadPodAfter {
		return ""
	}
	cs, _ := Dig(p, "status", "containerStatuses").([]any)
	for _, c := range cs {
		if r := str(c, "state", "waiting", "reason"); badWaiting[r] {
			return fmt.Sprintf("pod %s: %s", str(p, "metadata", "name"), r)
		}
	}
	return ""
}

// ownedBy reports whether pod p is owned by the workload (Deployment pods are owned by a ReplicaSet named <deploy>-<hash>).
func ownedBy(p map[string]any, kind, name string) bool {
	refs, _ := Dig(p, "metadata", "ownerReferences").([]any)
	for _, r := range refs {
		on, ok := str(r, "name"), str(r, "kind")
		if kind == "Deployment" && ok == "ReplicaSet" {
			if i := strings.LastIndex(on, "-"); i > 0 && on[:i] == name {
				return true
			}
		} else if ok == kind && on == name {
			return true
		}
	}
	return false
}

// Health is the pure rollout health function (K8S-063, 08 criteria). plan is the revision's rendered objects,
// cached the informer objects of the stack (workloads and pods), rev the revision number: a cached workload whose
// control-8/revision label differs has not been observed yet, so it is not healthy. since is when the apply began.
func Health(plan, cached []map[string]any, rev int, since, now time.Time) HealthResult {
	res := HealthResult{Healthy: true}
	for _, want := range plan {
		kind := Kind(want)
		if kind != "Deployment" && kind != "StatefulSet" && kind != "DaemonSet" && kind != "Job" {
			continue // other kinds: applied without error is enough
		}
		name := str(want, "metadata", "name")
		w := WorkloadHealth{Kind: kind, Name: name}
		var o map[string]any
		for _, c := range cached {
			if Kind(c) == kind && str(c, "metadata", "name") == name {
				o = c
			}
		}
		switch {
		case o == nil:
			w.Reason = "not observed yet"
		case str(o, "metadata", "labels", "control-8/revision") != fmt.Sprint(rev):
			w.Reason = "waiting for revision to be observed"
		default:
			evalWorkload(&w, o)
		}
		if !w.Healthy && !w.Failed && o != nil {
			for _, p := range cached {
				if Kind(p) == "Pod" && ownedBy(p, kind, name) {
					if r := podFailure(p, since, now); r != "" {
						w.Reason, w.Failed = r, true
						break
					}
				}
			}
		}
		if !w.Healthy {
			res.Healthy = false
		}
		res.Workloads = append(res.Workloads, w)
		if w.Failed && res.Failed == nil {
			res.Failed = &res.Workloads[len(res.Workloads)-1]
		}
	}
	return res
}

func evalWorkload(w *WorkloadHealth, o map[string]any) {
	gen, obs := n(o, 0, "metadata", "generation"), n(o, 0, "status", "observedGeneration")
	switch w.Kind {
	case "Deployment":
		w.Desired = n(o, 1, "spec", "replicas")
		w.Updated, w.Ready, w.Available = n(o, 0, "status", "updatedReplicas"), n(o, 0, "status", "readyReplicas"), n(o, 0, "status", "availableReplicas")
		if st, r := cond(o, "Progressing"); st == "False" && r == "ProgressDeadlineExceeded" {
			w.Failed, w.Reason = true, "ProgressDeadlineExceeded"
			return
		}
		w.Healthy = obs >= gen && w.Updated == w.Desired && w.Available == w.Desired && n(o, 0, "status", "replicas") == w.Updated
	case "StatefulSet":
		w.Desired = n(o, 1, "spec", "replicas")
		w.Updated, w.Ready, w.Available = n(o, 0, "status", "updatedReplicas"), n(o, 0, "status", "readyReplicas"), n(o, 0, "status", "availableReplicas")
		ok := w.Ready == w.Desired
		if str(o, "spec", "updateStrategy", "type") != "OnDelete" {
			ok = ok && w.Updated == w.Desired && str(o, "status", "currentRevision") == str(o, "status", "updateRevision")
		}
		w.Healthy = obs >= gen && ok
	case "DaemonSet":
		w.Desired = n(o, 0, "status", "desiredNumberScheduled")
		w.Updated, w.Ready, w.Available = n(o, 0, "status", "updatedNumberScheduled"), n(o, 0, "status", "numberReady"), n(o, 0, "status", "numberAvailable")
		w.Healthy = obs >= gen && w.Updated == w.Desired && w.Available == w.Desired
	case "Job":
		w.Desired = n(o, 1, "spec", "completions")
		w.Updated, w.Ready, w.Available = n(o, 0, "status", "active"), n(o, 0, "status", "succeeded"), n(o, 0, "status", "succeeded")
		if st, r := cond(o, "Failed"); st == "True" {
			w.Failed, w.Reason = true, "job failed: "+r
			return
		}
		if st, _ := cond(o, "Complete"); st == "True" {
			w.Healthy = true
			return
		}
	}
	if !w.Healthy {
		w.Reason = fmt.Sprintf("%d/%d updated, %d/%d available", w.Updated, w.Desired, w.Available, w.Desired)
	}
}
