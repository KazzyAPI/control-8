package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/policy"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
)

func (s *server) stackRoutes(reg func(string, http.HandlerFunc)) {
	const st = "/api/v1/namespaces/{ns}/stacks"
	reg("GET "+st, s.api(s.listStacks))
	reg("POST "+st, s.api(s.createStack))
	reg("GET "+st+"/{stack}", s.api(s.getStack))
	reg("DELETE "+st+"/{stack}", s.api(s.deleteStack))
	reg("GET "+st+"/{stack}/revisions", s.api(s.listRevisions))
	reg("POST "+st+"/{stack}/revisions", s.api(s.createRevision))
	reg("GET "+st+"/{stack}/revisions/{n}", s.api(s.getRevision))
	reg("GET "+st+"/{stack}/revisions/{n}/diff", s.api(s.diffRevision))
	reg("GET /api/v1/policies/global", s.api(s.getGlobalPolicy))
	reg("PUT /api/v1/policies/global", s.api(s.putGlobalPolicy))
	reg("GET /api/v1/namespaces/{ns}/policy", s.api(s.getNamespacePolicy))
	reg("PUT /api/v1/namespaces/{ns}/policy", s.api(s.putNamespacePolicy))
	reg("GET /api/v1/namespaces/{ns}/policy/effective", s.api(s.getEffectivePolicy))
}

type stackRec struct {
	ID, NSID, NS, Asset, TargetID, Target, Slug, Runtime, State, Tier string
	K8sNS, Profile, Digest                                            *string
	Desired                                                           *int
	Frozen                                                            bool
	Drift                                                             string
}

const stackSel = `SELECT st.id,n.id,n.slug,a.slug,st.target_id,COALESCE(t.name,''),st.slug,st.runtime,st.state,
	CASE WHEN n.environment_tier='prod' OR t.environment_tier='prod' THEN 'prod' ELSE 'non-prod' END,
	st.k8s_namespace,st.safety_profile,st.digest_status,st.desired_revision,st.rollout_frozen,st.drift
	FROM stack st JOIN namespace n ON n.id=st.namespace_id JOIN asset a ON a.id=st.asset_id LEFT JOIN target t ON t.id=st.target_id `

func scanStack(row interface{ Scan(...any) error }) (x stackRec, err error) {
	err = row.Scan(&x.ID, &x.NSID, &x.NS, &x.Asset, &x.TargetID, &x.Target, &x.Slug, &x.Runtime, &x.State, &x.Tier,
		&x.K8sNS, &x.Profile, &x.Digest, &x.Desired, &x.Frozen, &x.Drift)
	return
}

// JSON is the Stack shape (03). Health is "frozen" or "unknown" until informers observe (M9, D-68).
func (x stackRec) JSON() map[string]any {
	health := "unknown"
	if x.Frozen {
		health = "frozen"
	}
	var digest any
	if x.Digest != nil {
		digest = json.RawMessage(*x.Digest)
	}
	return map[string]any{"id": x.ID, "namespace": x.NS, "slug": x.Slug, "asset": x.Asset, "target": x.Target, "runtime": x.Runtime,
		"environment_tier": x.Tier, "k8s_namespace": x.K8sNS, "state": x.State, "safety_profile": x.Profile, "desired_revision": x.Desired,
		"health": health, "rollout_frozen": x.Frozen, "drift": json.RawMessage(x.Drift), "digest_status": digest, "services": []any{}}
}

func (s *server) publishStack(x stackRec) {
	s.publish(events.Event{Type: "stack.updated", At: s.now(), Namespace: x.NS, Data: x.JSON()})
}

func (s *server) loadStack(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (stackRec, bool) {
	n, ok := s.loadNS(w, r, p, min, action)
	if !ok {
		return stackRec{}, false
	}
	x, err := scanStack(s.db.QueryRow(stackSel+`WHERE st.namespace_id=? AND st.slug=?`, n.ID, r.PathValue("stack")))
	if err != nil {
		problem(w, 404, "not_found", "not found")
		return x, false
	}
	return x, true
}

func (s *server) listStacks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "viewer", "stack.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := s.db.Query(stackSel+`WHERE st.namespace_id=? AND st.slug>? AND st.state!='removed' ORDER BY st.slug LIMIT ?`, n.ID, after, limit+1)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []map[string]any{}, (*string)(nil)
	for rows.Next() {
		x, _ := scanStack(rows)
		if len(items) == limit {
			next = cursor(items[limit-1]["slug"].(string))
			break
		}
		items = append(items, x.JSON())
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *server) getStack(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if x, ok := s.loadStack(w, r, p, "viewer", "stack.get"); ok {
		writeJSON(w, 200, x.JSON())
	}
}

func (s *server) createStack(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "operator", "stack.create")
	if !ok {
		return
	}
	var in struct{ Slug, Asset, Target, Runtime string }
	if !decode(w, r, &in) {
		return
	}
	if !slugRe.MatchString(in.Slug) || !slugRe.MatchString(in.Asset) || !slugRe.MatchString(in.Target) {
		problem(w, 400, "invalid_body", "slug, asset and target are required slugs")
		return
	}
	var assetID, managedBy, tid, kind string
	if s.db.QueryRow(`SELECT id,managed_by FROM asset WHERE namespace_id=? AND slug=?`, n.ID, in.Asset).Scan(&assetID, &managedBy) != nil {
		problem(w, 400, "unknown_asset", "no such asset in this namespace")
		return
	}
	if s.db.QueryRow(`SELECT id,kind FROM target WHERE name=?`, in.Target).Scan(&tid, &kind) != nil {
		problem(w, 400, "unknown_target", "no such target")
		return
	}
	switch {
	case managedBy == "argo": // DOM-005
		s.auditNS(r, p, n.Slug, "stack.create", "stack", in.Slug, "failed", map[string]any{"reason": "argo_managed"})
		problem(w, 409, "argo_managed", "asset is managed by Argo CD; it has no stacks")
		return
	case in.Runtime != "" && in.Runtime != kind: // K8S-004
		problem(w, 400, "runtime_target_mismatch", "runtime must equal the target kind ("+kind+")")
		return
	}
	id, now := auth.NewID("stk"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO stack(id,namespace_id,asset_id,target_id,slug,runtime,k8s_namespace,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, n.ID, assetID, tid, in.Slug, kind, n.Slug, now, now); err != nil {
		s.auditNS(r, p, n.Slug, "stack.create", "stack", in.Slug, "failed", nil)
		problem(w, 409, "stack_exists", "stack already exists")
		return
	}
	s.auditNS(r, p, n.Slug, "stack.create", "stack", id, "success", map[string]any{"slug": in.Slug, "asset": in.Asset, "target": in.Target})
	x, _ := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, id))
	s.publishStack(x)
	writeJSON(w, 201, x.JSON())
}

// deleteStack deletes a draft; any other state requests a teardown deployment (08 Teardown).
func (s *server) deleteStack(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "operator", "stack.delete")
	if !ok {
		return
	}
	if x.State != "draft" {
		if x.State == "removed" {
			problem(w, 409, "stack_removed", "stack was removed")
			return
		}
		s.teardown(w, r, p, x)
		return
	}
	s.db.Exec(`UPDATE stack SET state='removed',updated_at=? WHERE id=?`, store.Ts(s.now()), x.ID)
	s.auditNS(r, p, x.NS, "stack.delete", "stack", x.ID, "success", nil)
	s.publish(events.Event{Type: "stack.removed", At: s.now(), Namespace: x.NS, Data: map[string]string{"namespace": x.NS, "slug": x.Slug}})
	w.WriteHeader(204)
}

type revRec struct {
	ID             string          `json:"id"`
	Number         int             `json:"number"`
	Runtime        string          `json:"runtime"`
	Profile        string          `json:"safety_profile"`
	Origin         string          `json:"origin"`
	Base           *int            `json:"base_revision"`
	ManifestsInput *string         `json:"manifests_input,omitempty"`
	Rendered       json.RawMessage `json:"rendered,omitempty"`
	Inventory      json.RawMessage `json:"inventory"`
	Timeout        int             `json:"rollout_timeout_seconds"`
	DigestPoll     bool            `json:"digest_poll"`
	DBServices     json.RawMessage `json:"database_services"`
	Policy         json.RawMessage `json:"policy_result"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      string          `json:"created_at"`
}

const revCols = `id,number,safety_profile,origin,base_revision,manifests_input,rendered,inventory,rollout_timeout_seconds,digest_poll,database_services,policy_result,created_by,created_at`

func scanRev(row interface{ Scan(...any) error }, runtime string, full bool) (v revRec, err error) {
	var rendered, inv, dbs, pol string
	err = row.Scan(&v.ID, &v.Number, &v.Profile, &v.Origin, &v.Base, &v.ManifestsInput, &rendered, &inv, &v.Timeout, &v.DigestPoll, &dbs, &pol, &v.CreatedBy, &v.CreatedAt)
	v.Runtime, v.Inventory, v.DBServices, v.Policy = runtime, json.RawMessage(inv), json.RawMessage(dbs), json.RawMessage(pol)
	if full {
		v.Rendered = json.RawMessage(rendered)
	} else {
		v.ManifestsInput = nil // event/list shape: no rendered, no input
	}
	return
}

// rules returns the effective rules for a namespace (global merged with its optional policy).
func (s *server) rules(nsID string) (policy.Rules, error) {
	var g policy.Rules
	var raw string
	if err := s.db.QueryRow(`SELECT rules FROM policy WHERE id='pol_global'`).Scan(&raw); err != nil {
		return g, err
	}
	json.Unmarshal([]byte(raw), &g)
	var n *policy.Rules
	if s.db.QueryRow(`SELECT rules FROM policy WHERE namespace_id=?`, nsID).Scan(&raw) == nil {
		n = &policy.Rules{}
		json.Unmarshal([]byte(raw), n)
	}
	return policy.Merge(g, n), nil
}

func clone(objs []map[string]any) (out []map[string]any) {
	b, _ := json.Marshal(objs)
	json.Unmarshal(b, &out)
	return
}

func (s *server) createRevision(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "operator", "revision.create")
	if !ok {
		return
	}
	var in struct {
		Profile    string   `json:"safety_profile"`
		Manifests  *string  `json:"manifests"`
		Compose    *string  `json:"compose"`
		Timeout    *int     `json:"rollout_timeout_seconds"`
		DigestPoll bool     `json:"digest_poll"`
		DBServices []string `json:"database_services"`
	}
	if !decode(w, r, &in) {
		return
	}
	timeout := int(s.cfg.RolloutTimeout / time.Second)
	if timeout == 0 {
		timeout = 300
	}
	if in.Timeout != nil {
		timeout = *in.Timeout
	}
	switch {
	case x.State == "removed":
		problem(w, 409, "stack_removed", "stack was removed")
		return
	case in.Manifests == nil || in.Compose != nil: // exactly one spec, matching the runtime (kubernetes: manifests)
		problem(w, 400, "spec_runtime_mismatch", "a kubernetes stack takes manifests, not compose")
		return
	case in.Profile == "":
		problem(w, 400, "safety_profile_required", "safety_profile is required")
		return
	case in.Profile != "managed_rollout" && in.Profile != "as_is" && in.Profile != "parachute":
		problem(w, 400, "invalid_body", "unknown safety_profile")
		return
	case in.Profile == "parachute": // managed_rollout is kubernetes-only, parachute docker-only, as_is both
		problem(w, 400, "safety_profile_invalid_for_runtime", "parachute is docker-only")
		return
	case timeout < 30 || timeout > 1800:
		problem(w, 400, "invalid_body", "rollout_timeout_seconds must be 30..1800")
		return
	case in.DigestPoll && in.Profile != "managed_rollout":
		problem(w, 400, "invalid_body", "digest_poll needs managed_rollout")
		return
	}
	objs, err := k8s.Parse(*in.Manifests)
	if err != nil {
		s.auditNS(r, p, x.NS, "revision.create", "stack", x.ID, "failed", map[string]any{"reason": "invalid_manifests"})
		problem(w, 422, "invalid_manifests", err.Error())
		return
	}
	dbs := k8s.DetectDatabases(objs)
	for _, d := range in.DBServices {
		if !slices.ContainsFunc(objs, func(o map[string]any) bool { return k8s.IsWorkload(k8s.Kind(o)) && k8s.Name(o) == d }) {
			problem(w, 400, "invalid_body", "database_services entry "+d+" is not a workload of this revision")
			return
		}
		dbs = append(dbs, d)
	}
	slices.Sort(dbs)
	dbs = slices.Compact(dbs)
	if dbs == nil {
		dbs = []string{}
	}
	var number int
	s.db.QueryRow(`SELECT COALESCE(MAX(number),0)+1 FROM revision WHERE stack_id=?`, x.ID).Scan(&number)
	evalObjs := clone(objs) // policy sees the user's input, before labels are injected
	rendered, inv := k8s.Render(objs, x.ID, x.K8sNamespace(), number, in.Profile)
	pr, err := s.policyResult(x, evalObjs, rendered, in.Profile, dbs)
	if err != nil {
		problem(w, 500, "internal", "policy unavailable")
		return
	}
	id, now := auth.NewID("rev"), store.Ts(s.now())
	rj, ij, dj, pj := mj(rendered), mj(inv), mj(dbs), mj(pr)
	if _, err := s.db.Exec(`INSERT INTO revision(id,stack_id,number,safety_profile,manifests_input,rendered,inventory,rollout_timeout_seconds,digest_poll,database_services,policy_result,created_by,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, x.ID, number, in.Profile, *in.Manifests, rj, ij, timeout, in.DigestPoll, dj, pj, p.ID, now); err != nil {
		problem(w, 409, "revision_conflict", "concurrent revision; retry")
		return
	}
	s.auditNS(r, p, x.NS, "revision.create", "revision", id, "success", map[string]any{"stack": x.Slug, "number": number, "allowed": pr.Allowed})
	full, _ := scanRev(s.db.QueryRow(`SELECT `+revCols+` FROM revision WHERE id=?`, id), x.Runtime, true)
	ev := full
	ev.Rendered = nil
	s.publish(events.Event{Type: "revision.created", At: s.now(), Namespace: x.NS, Data: ev})
	writeJSON(w, 201, full)
}

// K8sNamespace is the mapped Kubernetes namespace (K8S-010).
func (x stackRec) K8sNamespace() string {
	if x.K8sNS != nil {
		return *x.K8sNS
	}
	return x.NS
}

func mj(v any) string { b, _ := json.Marshal(v); return string(b) }

// policyResult evaluates the revision; DBChanged follows POL-R9 (first apply, database spec or profile changed).
func (s *server) policyResult(x stackRec, in, rendered []map[string]any, profile string, dbs []string) (policy.Result, error) {
	rules, err := s.rules(x.NSID)
	if err != nil {
		return policy.Result{}, err
	}
	changed := x.Desired == nil || x.Profile == nil || *x.Profile != profile
	if !changed {
		var old string
		var oldObjs []map[string]any
		s.db.QueryRow(`SELECT rendered FROM revision WHERE stack_id=? AND number=?`, x.ID, *x.Desired).Scan(&old)
		json.Unmarshal([]byte(old), &oldObjs)
		spec := func(objs []map[string]any, d string) string {
			for _, o := range objs {
				if k8s.Name(o) == d {
					return mj(o["spec"])
				}
			}
			return ""
		}
		changed = slices.ContainsFunc(dbs, func(d string) bool { return spec(oldObjs, d) != spec(rendered, d) })
	}
	return policy.Evaluate(rules, policy.Input{K8sNamespace: x.K8sNamespace(), Profile: profile, Objects: in, DatabaseServices: dbs, DBChanged: changed}), nil
}

func (s *server) listRevisions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "viewer", "revision.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	last, _ := strconv.Atoi(after)
	rows, err := s.db.Query(`SELECT `+revCols+` FROM revision WHERE stack_id=? AND number>? ORDER BY number LIMIT ?`, x.ID, last, limit+1)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []revRec{}, (*string)(nil)
	for rows.Next() {
		v, _ := scanRev(rows, x.Runtime, false)
		if len(items) == limit {
			next = cursor(strconv.Itoa(items[limit-1].Number))
			break
		}
		items = append(items, v)
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *server) revision(w http.ResponseWriter, x stackRec, n string) (revRec, bool) {
	num, err := strconv.Atoi(n)
	if err == nil {
		var v revRec
		if v, err = scanRev(s.db.QueryRow(`SELECT `+revCols+` FROM revision WHERE stack_id=? AND number=?`, x.ID, num), x.Runtime, true); err == nil {
			return v, true
		}
	}
	problem(w, 404, "not_found", "no such revision")
	return revRec{}, false
}

func (s *server) getRevision(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if x, ok := s.loadStack(w, r, p, "viewer", "revision.get"); ok {
		if v, ok := s.revision(w, x, r.PathValue("n")); ok {
			writeJSON(w, 200, v)
		}
	}
}

// yamlOf renders a revision's objects as multi-document YAML lines.
func yamlOf(rendered json.RawMessage) []string {
	var objs []map[string]any
	json.Unmarshal(rendered, &objs)
	var out []string
	for i, o := range objs {
		b, _ := yaml.Marshal(o)
		if i > 0 {
			out = append(out, "---")
		}
		out = append(out, strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")...)
	}
	return out
}

// unified is a unified diff with a single hunk spanning both files (shortcut: no context trimming).
func unified(a, b []string, an, bn string) string {
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", an, bn)
	if len(a)+len(b) == 0 {
		return sb.String()
	}
	fmt.Fprintf(&sb, "@@ -1,%d +1,%d @@\n", len(a), len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			sb.WriteString(" " + a[i] + "\n")
			i++
			j++
		case j == len(b) || i < len(a) && t[i+1][j] >= t[i][j+1]:
			sb.WriteString("-" + a[i] + "\n")
			i++
		default:
			sb.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	return sb.String()
}

func (s *server) diffRevision(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "viewer", "revision.diff")
	if !ok {
		return
	}
	against := r.URL.Query().Get("against")
	if _, err := strconv.Atoi(against); err != nil {
		problem(w, 400, "invalid_query", "against must be a revision number")
		return
	}
	b, ok := s.revision(w, x, r.PathValue("n"))
	if !ok {
		return
	}
	a, ok := s.revision(w, x, against)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(unified(yamlOf(a.Rendered), yamlOf(b.Rendered), fmt.Sprint("revision ", a.Number), fmt.Sprint("revision ", b.Number))))
}

// --- policy endpoints (05) ---

func validRules(r policy.Rules) string {
	for _, p := range r.AllowedSafetyProfiles {
		if p != "managed_rollout" && p != "as_is" && p != "parachute" {
			return "unknown safety profile " + p
		}
	}
	for _, g := range r.AllowedK8sNamespaces {
		if i := strings.Index(g, "*"); g == "" || i >= 0 && i != len(g)-1 {
			return "allowed_k8s_namespaces entries are names or prefix* globs"
		}
	}
	for _, b := range r.AllowedBindPrefixes {
		if !strings.HasPrefix(b, "/") {
			return "allowed_bind_prefixes must be absolute paths"
		}
	}
	return ""
}

func (s *server) storedRules(where string, arg any) (rules policy.Rules, found bool) {
	var raw string
	if s.db.QueryRow(`SELECT rules FROM policy WHERE `+where, arg).Scan(&raw) == nil {
		json.Unmarshal([]byte(raw), &rules)
		found = true
	}
	return rules.Norm(), found
}

func (s *server) getGlobalPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	g, _ := s.storedRules(`id=?`, "pol_global")
	writeJSON(w, 200, g)
}

func (s *server) putGlobalPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.deny(w, r, p, "policy.update", err)
		return
	}
	s.putPolicy(w, r, p, "", "pol_global", "id=?", "pol_global")
}

func (s *server) getNamespacePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if n, ok := s.loadNS(w, r, p, "viewer", "policy.get"); ok {
		v, _ := s.storedRules(`namespace_id=?`, n.ID)
		writeJSON(w, 200, v)
	}
}

func (s *server) putNamespacePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "admin", "policy.update")
	if !ok {
		return
	}
	s.putPolicy(w, r, p, n.Slug, n.ID, "namespace_id=?", n.ID)
}

// putPolicy replaces a rules document and audits old and new (05). nsID "" = global row.
func (s *server) putPolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal, slug, key, where string, arg any) {
	var in policy.Rules
	if !decode(w, r, &in) {
		return
	}
	in = in.Norm()
	msg := validRules(in)
	if g, _ := s.storedRules(`id=?`, "pol_global"); msg == "" && slug != "" {
		msg = policy.Validate(g, in)
	}
	if msg != "" {
		problem(w, 400, "policy_invalid", msg)
		return
	}
	old, _ := s.storedRules(where, arg)
	now := store.Ts(s.now())
	if slug == "" {
		s.db.Exec(`UPDATE policy SET rules=?,updated_by=?,updated_at=? WHERE id=?`, mj(in), p.ID, now, key)
	} else {
		s.db.Exec(`INSERT INTO policy(id,scope,namespace_id,rules,updated_by,created_at,updated_at) VALUES(?,'namespace',?,?,?,?,?)
			ON CONFLICT(namespace_id) DO UPDATE SET rules=excluded.rules,updated_by=excluded.updated_by,updated_at=excluded.updated_at`,
			auth.NewID("pol"), key, mj(in), p.ID, now, now)
	}
	s.auditNS(r, p, slug, "policy.update", "policy", key, "success", map[string]any{"old": old, "new": in})
	writeJSON(w, 200, in)
}

func (s *server) getEffectivePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if n, ok := s.loadNS(w, r, p, "viewer", "policy.effective"); ok {
		e, err := s.rules(n.ID)
		if err != nil {
			problem(w, 500, "internal", "query failed")
			return
		}
		writeJSON(w, 200, e)
	}
}
