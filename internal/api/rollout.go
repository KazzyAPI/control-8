package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
)

// rolloutState is deployment.rollout (02). rolled_back_to is filled by the M11 rollback.
type rolloutState struct {
	State        string           `json:"state"`
	Result       *string          `json:"result"`
	Timeout      int              `json:"timeout_seconds"`
	Workloads    []map[string]any `json:"workloads"`
	RolledBackTo *int             `json:"rolled_back_to"`
}

// setRollout stores the rollout JSON; the caller publishes (progress is coalesced, EVT-005).
func (s *server) setRollout(depID string, r rolloutState) {
	b, _ := json.Marshal(r)
	s.db.Exec(`UPDATE deployment SET rollout=? WHERE id=?`, string(b), depID)
}

func workloadJSON(ws []k8s.WorkloadHealth) []map[string]any {
	out := []map[string]any{}
	for _, w := range ws {
		var reason any
		if w.Reason != "" {
			reason = w.Reason
		}
		out = append(out, map[string]any{"kind": w.Kind, "name": w.Name, "desired": w.Desired, "updated": w.Updated,
			"ready": w.Ready, "available": w.Available, "reason": reason})
	}
	return out
}

// rbMode says what a health wait guards: a plain apply, one that rolls back on failure (K8S-053), or the rollback itself.
type rbMode int

const (
	rbNone rbMode = iota
	rbMay
	rbRun
)

// rollbackDone records the end of a rollback (K8S-054/055): rollout result and the rollout.rollback audit.
func (s *server) rollbackDone(d depRec, x stackRec, ro rolloutState, prev int, ok bool) {
	res, outcome := "rolled_back", "success"
	if !ok {
		res, outcome = "rollback_failed", "failed"
	}
	ro.State, ro.Result, ro.RolledBackTo = "done", &res, &prev
	s.setRollout(d.ID, ro)
	s.auditSys(d.NS, "rollout.rollback", d.ID, outcome, map[string]any{"stack": x.Slug, "rolled_back_to": prev, "result": res})
}

// waitHealthy evaluates rollout health on every informer change of the stack plus a 5 s safety tick (K8S-063)
// until healthy, immediately unhealthy, or the revision's rollout timeout. Without informers (no inventory
// engine) there is nothing to observe and the wait is skipped.
func (s *server) waitHealthy(ctx context.Context, d depRec, x stackRec, plan []map[string]any, rev, timeout int, rb rbMode) error {
	if s.inv == nil {
		return nil
	}
	start := s.now()
	deadline := start.Add(time.Duration(timeout) * time.Second)
	sig, cancel := s.inv.Subscribe(x.ID)
	defer cancel()
	tick, stop := s.tick(5 * time.Second)
	defer stop()
	ro, last := rolloutState{State: "waiting", Timeout: timeout}, ""
	if rb == rbRun {
		ro.State, ro.RolledBackTo = "rolling_back", &rev
	}
	done := func(result, action, outcome string, det map[string]any) {
		switch {
		case rb == rbRun: // rev is the revision rolled back to
			s.rollbackDone(d, x, ro, rev, result == "healthy")
			return
		case rb == rbMay && result == "failed":
			ro.State, ro.Result = "rolling_back", nil // the executor rolls back next
		default:
			ro.State, ro.Result = "done", &result
		}
		s.setRollout(d.ID, ro)
		det["stack"], det["revision"] = x.Slug, rev
		s.auditSys(d.NS, action, d.ID, outcome, det)
	}
	for {
		var objs []map[string]any
		if c := s.inv.Cache(x.Target); c != nil {
			objs = c.Objects(x.ID)
		}
		h := k8s.Health(plan, objs, rev, start, s.now())
		ro.Workloads = workloadJSON(h.Workloads)
		if b, _ := json.Marshal(ro); string(b) != last {
			last = string(b)
			s.setRollout(d.ID, ro)
			s.progress.Do(d.ID, func() { s.publishDep(d.ID) })
		}
		var fail *k8s.WorkloadHealth
		code, why := "rollout_unhealthy", ""
		switch {
		case h.Healthy:
			done("healthy", "rollout.healthy", "success", map[string]any{})
			return nil
		case h.Failed != nil:
			fail = h.Failed
		case !s.now().Before(deadline) || ctx.Err() != nil:
			code, why = "rollout_timeout", fmt.Sprintf("not healthy after %ds: ", timeout)
			for i := range h.Workloads {
				if !h.Workloads[i].Healthy {
					fail = &h.Workloads[i]
					break
				}
			}
		}
		if fail != nil {
			why += fail.Kind + "/" + fail.Name + ": " + fail.Reason
			done("failed", "rollout.failed", "failed", map[string]any{"workload": fail.Kind + "/" + fail.Name, "reason": fail.Reason, "error": code})
			return &rt.Error{Code: code, Detail: why}
		}
		select {
		case <-sig:
		case <-tick:
		case <-ctx.Done():
		}
	}
}
