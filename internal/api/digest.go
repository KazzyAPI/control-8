package api

import (
	"fmt"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/digest"
	"github.com/KazzyAPI/control-8/internal/store"
)

// WithDigest wires the digest poller to this server: stack.updated on change, digest_poll.detected audits and
// system-actor image updates (K8S-051). The caller starts p.Run.
func WithDigest(p *digest.Poller) Option {
	return func(s *server) {
		p.Now = s.now
		p.Changed = func(id string) {
			if x, err := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, id)); err == nil {
				s.publishStack(x)
			}
		}
		p.Audit = func(ns, id string, d map[string]any) {
			s.auditSystem(ns, "digest_poll.detected", "stack", id, "success", d)
		}
		p.Request = s.systemImageUpdate
	}
}

// systemImageUpdate is the non-prod digest-poll update: the manual image-update flow with actor system (no grant needed off prod).
func (s *server) systemImageUpdate(stackID, workload, container, image string) error {
	x, err := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, stackID))
	if err != nil || x.Tier == "prod" || x.Desired == nil || x.Frozen {
		return fmt.Errorf("stack not eligible")
	}
	rv, e := s.createImageUpdate(x, workload, container, image, "system")
	if e != nil {
		s.auditSystem(x.NS, "image_update.request", "stack", x.ID, "failed", e.audit)
		return fmt.Errorf("%s", e.code)
	}
	s.auditSystem(x.NS, "image_update.request", "revision", rv.id, "success", rv.details(x, workload, container, image))
	s.publishRevision(x, rv.id)
	status := "queued"
	if rv.approval {
		status = "pending_approval"
	}
	id, now := auth.NewID("dep"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO deployment(id,stack_id,revision_id,action,trigger,status,requested_by,reason,created_at,updated_at)
		VALUES(?,?,?,'apply','digest_poll',?,'system','digest poll',?,?)`, id, x.ID, rv.id, status, now, now); err != nil {
		return err
	}
	s.auditSystem(x.NS, "deployment.request", "deployment", id, "success", map[string]any{"stack": x.Slug, "action": "apply", "status": status})
	s.publishDep(id)
	if status == "queued" {
		s.dispatch(id)
	}
	return nil
}
