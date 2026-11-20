package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazzyAPI/control-8/internal/audit"
	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/policy"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
)

// DriverFunc returns the runtime driver for a configured target name.
type DriverFunc func(target string) (rt.Driver, error)

func (s *server) depRoutes(reg func(string, http.HandlerFunc)) {
	const d = "/api/v1/namespaces/{ns}/stacks/{stack}/deployments"
	reg("GET "+d, s.api(s.listDeployments))
	reg("POST "+d, s.api(s.requestDeployment))
	reg("GET /api/v1/deployments/{id}", s.api(s.getDeployment))
	reg("POST /api/v1/deployments/{id}/approve", s.api(s.approveDeployment))
	reg("POST /api/v1/deployments/{id}/reject", s.api(s.rejectDeployment))
	reg("POST /api/v1/deployments/{id}/cancel", s.api(s.cancelDeployment))
}

type depRec struct {
	ID, StackID, NS, Stack, Action, Trigger, Runtime, Tier, Status, RequestedBy string
	Revision                                                                    *int
	Profile, ApprovedBy, GrantID, GrantBypass, Reason, Rollout                  *string
	Started, Finished, Error                                                    *string
	Output                                                                      string
	Rowid                                                                       int64
}

const depSel = `SELECT d.id,d.stack_id,n.slug,st.slug,d.action,d.trigger,r.number,st.runtime,COALESCE(r.safety_profile,st.safety_profile),
	CASE WHEN n.environment_tier='prod' OR t.environment_tier='prod' THEN 'prod' ELSE 'non-prod' END,
	d.status,d.requested_by,d.approved_by,d.grant_id,d.grant_bypass,d.reason,d.rollout,d.started_at,d.finished_at,d.error,d.output,d.rowid
	FROM deployment d JOIN stack st ON st.id=d.stack_id JOIN namespace n ON n.id=st.namespace_id
	LEFT JOIN target t ON t.id=st.target_id LEFT JOIN revision r ON r.id=d.revision_id `

func scanDep(row interface{ Scan(...any) error }) (d depRec, err error) {
	err = row.Scan(&d.ID, &d.StackID, &d.NS, &d.Stack, &d.Action, &d.Trigger, &d.Revision, &d.Runtime, &d.Profile, &d.Tier,
		&d.Status, &d.RequestedBy, &d.ApprovedBy, &d.GrantID, &d.GrantBypass, &d.Reason, &d.Rollout, &d.Started, &d.Finished, &d.Error, &d.Output, &d.Rowid)
	return
}

// JSON is the Deployment shape (03); events omit output (AUTH-043).
func (d depRec) JSON(withOutput bool) map[string]any {
	var rollout any
	if d.Rollout != nil {
		rollout = json.RawMessage(*d.Rollout)
	}
	m := map[string]any{"id": d.ID, "namespace": d.NS, "stack": d.Stack, "action": d.Action, "trigger": d.Trigger, "revision": d.Revision,
		"runtime": d.Runtime, "safety_profile": d.Profile, "environment_tier": d.Tier, "status": d.Status, "requested_by": d.RequestedBy,
		"approved_by": d.ApprovedBy, "grant_id": d.GrantID, "grant_bypass": d.GrantBypass, "reason": d.Reason, "rollout": rollout,
		"started_at": d.Started, "finished_at": d.Finished, "error": d.Error}
	if withOutput {
		m["output"] = d.Output
	}
	return m
}

// publishDep emits deployment.updated on every status change; transitions are never coalesced (EVT-005 is for M10 progress).
func (s *server) publishDep(id string) {
	if d, err := scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, id)); err == nil {
		s.publish(events.Event{Type: "deployment.updated", At: s.now(), Namespace: d.NS, Data: d.JSON(false)})
	}
}

// auditSys records a worker outcome as a system action and publishes audit.created.
func (s *server) auditSys(ns, action, depID, outcome string, d map[string]any) {
	if a, err := audit.Insert(s.db, audit.Event{Namespace: ns, Action: action, TargetType: "deployment", TargetID: depID, Outcome: outcome, Details: d}); err == nil {
		s.publish(events.Event{Type: "audit.created", At: s.now(), Namespace: ns, Data: a})
	}
}

func (s *server) loadDep(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (depRec, bool) {
	d, err := scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, r.PathValue("id")))
	if err != nil {
		problem(w, 404, "not_found", "not found")
		return d, false
	}
	if err := authz.Require(s.db, p, d.NS, min); err != nil {
		s.denyNS(w, r, p, d.NS, action, err)
		return d, false
	}
	return d, true
}

func (s *server) getDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if d, ok := s.loadDep(w, r, p, "viewer", "deployment.get"); ok {
		writeJSON(w, 200, d.JSON(true))
	}
}

func (s *server) listDeployments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "viewer", "deployment.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	before := int64(1 << 62)
	if after != "" {
		if before, _ = strconv.ParseInt(after, 10, 64); before == 0 {
			problem(w, 400, "invalid_query", "bad cursor")
			return
		}
	}
	rows, err := s.db.Query(depSel+`WHERE d.stack_id=? AND d.rowid<? ORDER BY d.rowid DESC LIMIT ?`, x.ID, before, limit+1)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []map[string]any{}, (*string)(nil)
	for rows.Next() {
		d, _ := scanDep(rows)
		if len(items) == limit {
			next = cursor(strconv.FormatInt(items[limit-1]["_rowid"].(int64), 10))
			break
		}
		m := d.JSON(false)
		m["_rowid"] = d.Rowid
		items = append(items, m)
	}
	for _, m := range items {
		delete(m, "_rowid")
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

// gate runs the checks shared by apply and teardown requests; it writes the problem and returns false on refusal.
// Prod needs an access grant (M8 adds them): until then prod stacks refuse every deployment (D-71).
func (s *server) gate(w http.ResponseWriter, r *http.Request, p *auth.Principal, x stackRec, action, confirmTier string) bool {
	var managed string
	s.db.QueryRow(`SELECT a.managed_by FROM asset a JOIN stack st ON st.asset_id=a.id WHERE st.id=?`, x.ID).Scan(&managed)
	switch {
	case x.State == "removed":
		problem(w, 409, "stack_removed", "stack was removed")
	case managed == "argo": // D-22, K8S-044: Argo assets are register-only
		s.auditNS(r, p, x.NS, action, "stack", x.ID, "failed", map[string]any{"reason": "argo_managed"})
		problem(w, 409, "argo_managed", "asset is managed by Argo CD")
	case confirmTier != x.Tier:
		problem(w, 400, "environment_tier_mismatch", "confirm_environment_tier must be "+x.Tier)
	case x.Tier == "prod":
		s.auditNS(r, p, x.NS, action, "stack", x.ID, "denied", map[string]any{"reason": "grant_required"})
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "Forbidden", "status": 403, "code": "grant_required",
			"detail": "prod deployments need an active access grant", "scope": map[string]string{"target": x.Target, "namespace": x.NS}})
	case s.targets != nil && !s.reachable(x.Target):
		problem(w, 503, "target_unreachable", "target is not reachable")
	default:
		return true
	}
	return false
}

func (s *server) reachable(target string) bool {
	t, ok := s.targets.Get(target)
	return ok && t.Reachable
}

// enqueue inserts the deployment (DOM-001 is enforced by the unique index) and answers 202.
func (s *server) enqueue(w http.ResponseWriter, r *http.Request, p *auth.Principal, x stackRec, action string, revID *string, reason string, approval bool) {
	status := "queued"
	if approval {
		status = "pending_approval"
	}
	id, now := auth.NewID("dep"), store.Ts(s.now())
	var rs any
	if reason != "" {
		rs = reason
	}
	if _, err := s.db.Exec(`INSERT INTO deployment(id,stack_id,revision_id,action,status,requested_by,reason,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, x.ID, revID, action, status, p.ID, rs, now, now); err != nil {
		s.auditNS(r, p, x.NS, "deployment.request", "stack", x.ID, "failed", map[string]any{"reason": "deployment_in_progress"})
		problem(w, 409, "deployment_in_progress", "the stack already has a queued or running deployment")
		return
	}
	s.auditNS(r, p, x.NS, "deployment.request", "deployment", id, "success", map[string]any{"stack": x.Slug, "action": action, "status": status})
	s.publishDep(id)
	if status == "queued" {
		s.dispatch(id)
	}
	d, _ := scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, id))
	w.Header().Set("Location", "/api/v1/deployments/"+id)
	writeJSON(w, 202, d.JSON(true))
}

func (s *server) requestDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "operator", "deployment.request")
	if !ok {
		return
	}
	var in struct {
		Revision int    `json:"revision"`
		Profile  string `json:"confirm_safety_profile"`
		Tier     string `json:"confirm_environment_tier"`
		Reason   string `json:"reason"`
		Repull   *bool  `json:"repull"`
	}
	if !decode(w, r, &in) {
		return
	}
	var revID, profile, input, rendered, dbs, stored string
	if in.Revision < 1 || s.db.QueryRow(`SELECT id,safety_profile,manifests_input,rendered,database_services,policy_result FROM revision WHERE stack_id=? AND number=?`,
		x.ID, in.Revision).Scan(&revID, &profile, &input, &rendered, &dbs, &stored) != nil {
		problem(w, 404, "not_found", "no such revision")
		return
	}
	if !s.gate(w, r, p, x, "deployment.request", in.Tier) {
		return
	}
	if in.Profile != profile {
		problem(w, 400, "safety_profile_mismatch", "confirm_safety_profile must be "+profile)
		return
	}
	// DOM-003 + re-evaluation at request time (05): stored result first, then today's rules.
	var prev policy.Result
	json.Unmarshal([]byte(stored), &prev)
	var rend []map[string]any
	var dbl []string
	json.Unmarshal([]byte(rendered), &rend)
	json.Unmarshal([]byte(dbs), &dbl)
	objs, _ := k8s.Parse(input)
	res, err := s.policyResult(x, objs, rend, profile, dbl)
	if err != nil {
		problem(w, 500, "internal", "policy unavailable")
		return
	}
	if !prev.Allowed || !res.Allowed {
		if !res.Allowed {
			prev = res
		}
		var rules []string
		for _, v := range prev.Violations {
			rules = append(rules, v.Rule)
		}
		s.auditNS(r, p, x.NS, "deployment.request", "stack", x.ID, "failed", map[string]any{"reason": "policy_violation", "violations": rules})
		problem(w, 422, "policy_violation", "revision violates policy: "+strings.Join(rules, ", "))
		return
	}
	s.enqueue(w, r, p, x, "apply", &revID, in.Reason, res.RequiresApproval)
}

// teardown is DELETE on a deployed stack: reason required, POL-R10 sends database stacks to approval.
func (s *server) teardown(w http.ResponseWriter, r *http.Request, p *auth.Principal, x stackRec) {
	var in struct {
		Reason string `json:"reason"`
		Tier   string `json:"confirm_environment_tier"`
	}
	if r.Body != http.NoBody && !decode(w, r, &in) {
		return
	}
	if in.Tier == "" {
		in.Tier = x.Tier // optional on teardown (openapi); a stated tier must still match
	}
	if strings.TrimSpace(in.Reason) == "" {
		problem(w, 400, "invalid_body", "reason is required for teardown")
		return
	}
	if !s.gate(w, r, p, x, "deployment.request", in.Tier) {
		return
	}
	rules, err := s.rules(x.NSID)
	if err != nil {
		problem(w, 500, "internal", "policy unavailable")
		return
	}
	var dbs string
	if x.Desired != nil {
		s.db.QueryRow(`SELECT database_services FROM revision WHERE stack_id=? AND number=?`, x.ID, *x.Desired).Scan(&dbs)
	}
	var dbl []string
	json.Unmarshal([]byte(dbs), &dbl)
	s.enqueue(w, r, p, x, "teardown", nil, in.Reason, rules.ProtectedDBApproval && len(dbl) > 0)
}

func (s *server) approveDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	d, ok := s.loadDep(w, r, p, "admin", "deployment.approve")
	if !ok {
		return
	}
	switch {
	case d.Status != "pending_approval":
		problem(w, 409, "invalid_state", "deployment is not pending approval")
		return
	case p.ID == d.RequestedBy: // DOM-004
		s.auditNS(r, p, d.NS, "deployment.approve", "deployment", d.ID, "denied", map[string]any{"reason": "self_approval"})
		problem(w, 403, "self_approval", "the approver must differ from the requester")
		return
	case d.Tier == "prod": // grants arrive in M8 (D-71)
		s.auditNS(r, p, d.NS, "deployment.approve", "deployment", d.ID, "denied", map[string]any{"reason": "grant_required"})
		problem(w, 403, "grant_required", "prod deployments need an active access grant")
		return
	}
	res, err := s.db.Exec(`UPDATE deployment SET status='queued',approved_by=?,updated_at=? WHERE id=? AND status='pending_approval'`, p.ID, store.Ts(s.now()), d.ID)
	if err != nil {
		s.auditNS(r, p, d.NS, "deployment.approve", "deployment", d.ID, "failed", map[string]any{"reason": "deployment_in_progress"})
		problem(w, 409, "deployment_in_progress", "the stack already has a queued or running deployment")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		problem(w, 409, "invalid_state", "deployment is not pending approval")
		return
	}
	s.auditNS(r, p, d.NS, "deployment.approve", "deployment", d.ID, "success", nil)
	s.publishDep(d.ID)
	s.dispatch(d.ID)
	d, _ = scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, d.ID))
	writeJSON(w, 200, d.JSON(true))
}

func (s *server) rejectDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	d, ok := s.loadDep(w, r, p, "admin", "deployment.reject")
	if !ok {
		return
	}
	var in struct{ Reason string }
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		problem(w, 400, "invalid_body", "reason is required")
		return
	}
	s.finishManual(w, r, p, d, "rejected", "pending_approval", "deployment.reject", "rejected: "+in.Reason)
}

func (s *server) cancelDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	d, ok := s.loadDep(w, r, p, "operator", "deployment.cancel")
	if !ok {
		return
	}
	if p.ID != d.RequestedBy { // requester or admin
		if err := authz.Require(s.db, p, d.NS, "admin"); err != nil {
			s.denyNS(w, r, p, d.NS, "deployment.cancel", err)
			return
		}
	}
	s.finishManual(w, r, p, d, "cancelled", "pending_approval", "deployment.cancel", "")
}

// finishManual moves a not-yet-running deployment to a terminal status atomically against the worker.
func (s *server) finishManual(w http.ResponseWriter, r *http.Request, p *auth.Principal, d depRec, to, from, action, errText string) {
	states := []any{from}
	if to == "cancelled" {
		states = append(states, "queued")
	}
	q := `UPDATE deployment SET status=?,finished_at=?,error=?,updated_at=? WHERE id=? AND status IN (?` + strings.Repeat(",?", len(states)-1) + `)`
	var e any
	if errText != "" {
		e = errText
	}
	now := store.Ts(s.now())
	res, _ := s.db.Exec(q, append([]any{to, now, e, now, d.ID}, states...)...)
	if n, _ := res.RowsAffected(); n == 0 {
		s.auditNS(r, p, d.NS, action, "deployment", d.ID, "failed", map[string]any{"status": d.Status})
		problem(w, 409, "invalid_state", "deployment cannot be "+to+" in its current state")
		return
	}
	s.auditNS(r, p, d.NS, action, "deployment", d.ID, "success", nil)
	s.publishDep(d.ID)
	d, _ = scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, d.ID))
	writeJSON(w, 200, d.JSON(true))
}

// --- worker ---

func (s *server) stackLock(id string) *sync.Mutex {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (s *server) dispatch(id string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.execute(id)
	}()
}

// recoverDeployments runs at startup: a deployment left running by a crash is failed (its effect is unknown),
// queued ones are picked up again.
func (s *server) recoverDeployments() {
	now := store.Ts(s.now())
	s.db.Exec(`UPDATE deployment SET status='failed',error='interrupted: control 8 restarted',finished_at=?,updated_at=? WHERE status='running'`, now, now)
	rows, err := s.db.Query(`SELECT id FROM deployment WHERE status='queued' ORDER BY rowid`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		s.dispatch(id)
	}
}

// execute runs one queued deployment under its stack's mutex (DOM-001): precheck, apply, prune, or teardown.
func (s *server) execute(id string) {
	d, err := scanDep(s.db.QueryRow(depSel+`WHERE d.id=?`, id))
	if err != nil {
		return
	}
	mu := s.stackLock(d.StackID)
	mu.Lock()
	defer mu.Unlock()
	now := store.Ts(s.now())
	if res, _ := s.db.Exec(`UPDATE deployment SET status='running',started_at=?,updated_at=? WHERE id=? AND status='queued'`, now, now, id); res != nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return // cancelled while queued
		}
	}
	s.publishDep(id)
	x, _ := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, d.StackID))
	var rend []map[string]any
	var number int
	var profile string
	if d.Action == "apply" {
		var raw string
		s.db.QueryRow(`SELECT r.rendered,r.number,r.safety_profile FROM deployment d JOIN revision r ON r.id=d.revision_id WHERE d.id=?`, id).Scan(&raw, &number, &profile)
		json.Unmarshal([]byte(raw), &rend)
	}
	var out string
	wrote := false
	runErr := func() error {
		if s.drivers == nil {
			return &rt.Error{Code: "target_unreachable", Detail: "no driver for target"}
		}
		drv, err := s.drivers(x.Target)
		if err != nil {
			return &rt.Error{Code: "target_unreachable", Detail: err.Error()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // shortcut: fixed bound; M10 uses the revision's rollout timeout
		defer cancel()
		stack := rt.Stack{ID: x.ID, Namespace: x.K8sNamespace()}
		if d.Action == "teardown" {
			wrote = true
			out, err = drv.Teardown(ctx, stack)
			return err
		}
		plan := rt.Plan{Stack: stack, Objects: rend}
		if err := drv.Precheck(ctx, plan); err != nil {
			return err
		}
		wrote = true
		if out, err = drv.Apply(ctx, plan); err != nil {
			return err
		}
		pruned, err := drv.Prune(ctx, plan)
		out += pruned
		return err
	}()
	s.finish(d, x, number, profile, out, runErr, wrote)
}

func (s *server) finish(d depRec, x stackRec, number int, profile, out string, runErr error, wrote bool) {
	if len(out) > 64<<10 {
		out = out[len(out)-64<<10:]
	}
	status, now := "succeeded", store.Ts(s.now())
	var errText any
	details := map[string]any{"stack": x.Slug, "action": d.Action}
	outcome := "success"
	if runErr != nil {
		status, outcome, errText = "failed", "failed", runErr.Error()
		if e, ok := runErr.(*rt.Error); ok {
			details["error"] = e.Code
		}
	}
	s.db.Exec(`UPDATE deployment SET status=?,finished_at=?,output=?,error=?,updated_at=? WHERE id=?`, status, now, out, errText, now, d.ID)
	switch {
	case runErr == nil && d.Action == "teardown":
		s.db.Exec(`UPDATE stack SET state='removed',updated_at=? WHERE id=?`, now, x.ID)
	case runErr == nil:
		s.db.Exec(`UPDATE stack SET state='active',desired_revision=?,safety_profile=?,updated_at=? WHERE id=?`, number, profile, now, x.ID)
	case wrote && d.Action == "apply": // partially applied: as_is leaves the cluster as it is (08)
		s.db.Exec(`UPDATE stack SET state='failed',updated_at=? WHERE id=?`, now, x.ID)
	}
	s.auditSys(d.NS, "deployment.run", d.ID, outcome, details)
	s.publishDep(d.ID)
	if nx, err := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, x.ID)); err == nil {
		if nx.State == "removed" {
			s.publish(events.Event{Type: "stack.removed", At: s.now(), Namespace: nx.NS, Data: map[string]string{"namespace": nx.NS, "slug": nx.Slug}})
		} else if runErr == nil || nx.State != x.State {
			s.publishStack(nx)
		}
	}
}
