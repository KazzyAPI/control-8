package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/events"
	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
)

// imageRepo strips the digest and the tag: "ghcr.io/a/b:1.4@sha256:x" -> "ghcr.io/a/b" (a ":" before the last "/" is a registry port).
func imageRepo(img string) string {
	img, _, _ = strings.Cut(img, "@")
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		img = img[:i]
	}
	return img
}

// requestImageUpdate is K8S-050/052: revision N+1 = desired revision with one container image replaced
// (origin=image_update), policy re-run, then a deployment with trigger image_update.
func (s *server) requestImageUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadStack(w, r, p, "operator", "image_update.request")
	if !ok {
		return
	}
	var in struct {
		Workload  string `json:"workload"`
		Container string `json:"container"`
		Image     string `json:"image"`
		Reason    string `json:"reason"`
		Tier      string `json:"confirm_environment_tier"`
	}
	if !decode(w, r, &in) {
		return
	}
	fail := func(code int, c, msg string) {
		s.auditNS(r, p, x.NS, "image_update.request", "stack", x.ID, "failed", map[string]any{"reason": c})
		problem(w, code, c, msg)
	}
	switch {
	case in.Workload == "" || in.Container == "" || strings.TrimSpace(in.Image) == "" || strings.TrimSpace(in.Reason) == "":
		problem(w, 400, "invalid_body", "workload, container, image and reason are required")
		return
	case x.Runtime != "kubernetes" || x.Profile == nil || *x.Profile != "managed_rollout" || x.State != "active" || x.Desired == nil:
		fail(409, "image_update_not_allowed", "image updates need an active kubernetes managed_rollout stack")
		return
	case x.Frozen:
		fail(409, "rollout_frozen", "rollout is frozen after a failed rollback; an admin must deploy")
		return
	}
	gr, ok := s.gate(w, r, p, x, "image_update.request", in.Tier)
	if !ok {
		return
	}
	rv, e := s.createImageUpdate(x, in.Workload, in.Container, in.Image, p.ID)
	if e != nil {
		if e.audit != nil {
			s.auditNS(r, p, x.NS, "image_update.request", "stack", x.ID, "failed", e.audit)
		}
		problem(w, e.status, e.code, e.msg)
		return
	}
	s.auditNS(r, p, x.NS, "image_update.request", "revision", rv.id, "success", gr.details(rv.details(x, in.Workload, in.Container, in.Image)))
	s.publishRevision(x, rv.id)
	s.enqueue(w, r, p, x, gr, "apply", "image_update", &rv.id, in.Reason, rv.approval)
}

// updErr is a refused image update; audit (when set) is the details of the "failed" audit event.
type updErr struct {
	status    int
	code, msg string
	audit     map[string]any
}

type newRev struct {
	id           string
	number, base int
	old          string
	approval     bool
}

func (v newRev) details(x stackRec, workload, container, image string) map[string]any {
	return map[string]any{"stack": x.Slug, "workload": workload, "container": container, "from": v.old, "to": image, "revision": v.number, "base_revision": v.base}
}

func (s *server) publishRevision(x stackRec, id string) {
	full, _ := scanRev(s.db.QueryRow(`SELECT `+revCols+` FROM revision WHERE id=?`, id), x.Runtime, false)
	s.publish(events.Event{Type: "revision.created", At: s.now(), Namespace: x.NS, Data: full})
}

// createImageUpdate builds revision N+1 (desired with one container image replaced), re-runs policy and stores it
// (K8S-052). Shared by the manual route and the digest poll; actor is the revision's created_by.
func (s *server) createImageUpdate(x stackRec, workload, container, image, actor string) (newRev, *updErr) {
	fail := func(code int, c, msg string) (newRev, *updErr) {
		return newRev{}, &updErr{code, c, msg, map[string]any{"reason": c}}
	}
	var input, profile, dbs string
	var timeout int
	var poll bool
	s.db.QueryRow(`SELECT manifests_input,safety_profile,rollout_timeout_seconds,digest_poll,database_services FROM revision WHERE stack_id=? AND number=?`,
		x.ID, *x.Desired).Scan(&input, &profile, &timeout, &poll, &dbs)
	objs, err := k8s.Parse(input)
	if err != nil {
		return newRev{}, &updErr{500, "internal", "desired revision unreadable", nil}
	}
	var cont map[string]any
	for _, o := range objs {
		if k8s.Name(o) != workload {
			continue
		}
		for _, c := range k8s.Containers(o) {
			if c["name"] == container {
				cont = c
			}
		}
	}
	if cont == nil {
		return fail(404, "not_found", "no such workload or container in the desired revision")
	}
	old, _ := cont["image"].(string)
	if imageRepo(old) != imageRepo(image) { // a repo change is a structural revision
		return fail(400, "image_repo_change", "image must keep the repository "+imageRepo(old))
	}
	cont["image"] = image
	b, _ := yaml.Marshal(objs[0])
	docs := []string{string(b)}
	for _, o := range objs[1:] {
		b, _ := yaml.Marshal(o)
		docs = append(docs, string(b))
	}
	newInput := strings.Join(docs, "---\n")
	var dbl []string
	json.Unmarshal([]byte(dbs), &dbl)
	var open int
	s.db.QueryRow(`SELECT count(*) FROM deployment WHERE stack_id=? AND status IN ('queued','running')`, x.ID).Scan(&open)
	if open > 0 {
		return fail(409, "deployment_in_progress", "the stack already has a queued or running deployment")
	}
	var number int
	s.db.QueryRow(`SELECT COALESCE(MAX(number),0)+1 FROM revision WHERE stack_id=?`, x.ID).Scan(&number)
	evalObjs := clone(objs)
	rendered, inv := k8s.Render(objs, x.ID, x.K8sNamespace(), number, profile)
	pr, err := s.policyResult(x, evalObjs, rendered, profile, dbl)
	if err != nil {
		return newRev{}, &updErr{500, "internal", "policy unavailable", nil}
	}
	if !pr.Allowed {
		var rules []string
		for _, v := range pr.Violations {
			rules = append(rules, v.Rule)
		}
		return newRev{}, &updErr{422, "policy_violation", "revision violates policy: " + strings.Join(rules, ", "),
			map[string]any{"reason": "policy_violation", "violations": rules}}
	}
	id, now := auth.NewID("rev"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO revision(id,stack_id,number,safety_profile,origin,base_revision,manifests_input,rendered,inventory,rollout_timeout_seconds,digest_poll,database_services,policy_result,created_by,created_at)
		VALUES(?,?,?,?,'image_update',?,?,?,?,?,?,?,?,?,?)`, id, x.ID, number, profile, *x.Desired, newInput, mj(rendered), mj(inv), timeout, poll, mj(dbl), mj(pr), actor, now); err != nil {
		return newRev{}, &updErr{409, "revision_conflict", "concurrent revision; retry", nil}
	}
	return newRev{id, number, *x.Desired, old, pr.RequiresApproval}, nil
}
