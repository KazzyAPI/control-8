package api

import (
	"net/http"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/targets"
)

// publishTarget emits target.status (10 catalogue); no namespace, filtered by anyRole.
func (s *server) publishTarget(t targets.Target) {
	s.publish(events.Event{Type: "target.status", At: s.now(), Data: t})
}

// Read-only (D-49): any role in any namespace may list. Reads are not audited (D-62).
func (s *server) listTargets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.anyRole(p) {
		problem(w, 403, "forbidden", "no role in any namespace")
		return
	}
	items := []targets.Target{}
	if s.targets != nil {
		items = s.targets.List()
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) getTarget(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.anyRole(p) {
		problem(w, 403, "forbidden", "no role in any namespace")
		return
	}
	if s.targets != nil {
		if t, ok := s.targets.Get(r.PathValue("target")); ok {
			writeJSON(w, 200, t)
			return
		}
	}
	problem(w, 404, "not_found", "no such target")
}
