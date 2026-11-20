package api

import (
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
)

// routes registers everything and returns the patterns (the API-002 parity test walks them).
func (s *server) routes(mux *http.ServeMux) (patterns []string) {
	reg := func(pattern string, h http.HandlerFunc) {
		patterns = append(patterns, pattern)
		mux.HandleFunc(pattern, h)
	}
	reg("POST /auth/break-glass", s.breakGlass)
	reg("GET /auth/login", s.oidcLogin)
	reg("GET /auth/callback", s.oidcCallback)
	reg("GET /login", htmlPage("Sign in", `<p><a href="/auth/login">Sign in with OIDC</a></p>`))
	reg("GET /no-access", htmlPage("No access", "<p>No access: ask an admin to map your group.</p>"))
	reg("POST /auth/logout", s.api(s.logout))
	reg("GET /api/v1/events", s.api(s.stream))
	reg("GET /api/v1/me", s.api(s.me))
	reg("GET /api/v1/tokens", s.api(s.tokens))
	reg("GET /api/v1/role-bindings", s.api(s.listBindings))
	reg("POST /api/v1/role-bindings", s.api(s.createBinding))
	reg("DELETE /api/v1/role-bindings/{id}", s.api(s.deleteBinding))
	reg("GET /api/v1/targets", s.api(s.listTargets))
	reg("GET /api/v1/targets/{target}", s.api(s.getTarget))
	s.nsRoutes(reg)
	s.stackRoutes(reg)
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "not_found", "no such route") })
	return
}

// me lists namespace roles from bindings (null-namespace non-global roles cannot be enumerated until M2).
func (s *server) me(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	ns := []map[string]string{}
	rows, _ := s.db.Query(`SELECT DISTINCT namespace_id FROM role_binding WHERE namespace_id IS NOT NULL`)
	var slugs []string
	for rows != nil && rows.Next() {
		var n string
		rows.Scan(&n)
		slugs = append(slugs, n)
	}
	if rows != nil {
		rows.Close()
	}
	slices.Sort(slugs)
	for _, n := range slugs {
		if k := authz.Effective(s.db, p, n); k > 0 {
			ns = append(ns, map[string]string{"slug": n, "role": auth.Roles[k-1]})
		}
	}
	var csrf any
	if !p.IsToken() {
		csrf = p.CSRF
	}
	writeJSON(w, 200, map[string]any{"id": p.ID, "display_name": p.Display, "auth_method": p.AuthMethod,
		"global_admin": authz.Effective(s.db, p, "") == 4, "csrf_token": csrf,
		"approver_for": s.approverFor(p.Groups), "namespaces": ns, "active_grants": []any{}})
}

// tokens lists configured tokens: own (same owner), or all for global_admin. Never the hash (AUTH-033).
func (s *server) tokens(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	all := authz.Effective(s.db, p, "") == 4
	me := p.ID
	if p.IsToken() {
		var sub string
		s.db.QueryRow(`SELECT owner_sub FROM token WHERE id=?`, p.ID).Scan(&sub)
		me, _ = auth.OwnerOf(s.db, sub)
	}
	rows, err := s.db.Query(`SELECT id,name,owner_sub,namespace_id,role,expires_at,last_used_at,revoked_at FROM token ORDER BY name`)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	type row struct {
		id, name, sub, role, exp string
		ns, used, rev            *string
	}
	var list []row // the pool has one connection: drain rows before OwnerOf queries
	for rows.Next() {
		var x row
		rows.Scan(&x.id, &x.name, &x.sub, &x.ns, &x.role, &x.exp, &x.used, &x.rev)
		list = append(list, x)
	}
	rows.Close()
	items := []map[string]any{}
	for _, x := range list {
		id, name, sub, role, exp, ns, used, rev := x.id, x.name, x.sub, x.role, x.exp, x.ns, x.used, x.rev
		owner, _ := auth.OwnerOf(s.db, sub)
		if all || owner != "" && owner == me {
			items = append(items, map[string]any{"id": id, "name": name, "owner_sub": sub, "owner_resolved": owner != "",
				"namespace": ns, "role": role, "expires_at": exp, "last_used_at": used, "revoked_at": rev})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

type binding struct {
	ID        string  `json:"id"`
	IdpGroup  string  `json:"idp_group"`
	Namespace *string `json:"namespace"`
	Role      string  `json:"role"`
}

// adminOnly gates role-binding endpoints: global_admin, never tokens (AUTH-034), denials audited.
func (s *server) adminOnly(w http.ResponseWriter, r *http.Request, p *auth.Principal, action string) bool {
	if p.IsToken() {
		s.audit(r, p, action, "role_binding", "", "denied", nil)
		problem(w, 403, "token_not_allowed", "tokens cannot manage role bindings")
		return false
	}
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.deny(w, r, p, action, err)
		return false
	}
	return true
}

func (s *server) listBindings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.adminOnly(w, r, p, "role_binding.list") {
		return
	}
	rows, _ := s.db.Query(`SELECT id,idp_group,namespace_id,role FROM role_binding ORDER BY id`)
	defer rows.Close()
	items := []binding{}
	for rows.Next() {
		var b binding
		rows.Scan(&b.ID, &b.IdpGroup, &b.Namespace, &b.Role)
		items = append(items, b)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) createBinding(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.adminOnly(w, r, p, "role_binding.create") {
		return
	}
	var b binding
	if !decode(w, r, &b) {
		return
	}
	switch {
	case strings.TrimSpace(b.IdpGroup) == "" || !auth.ValidRole(b.Role):
		problem(w, 400, "invalid_body", "idp_group and a valid role are required")
		return
	case b.Namespace != nil && !slugRe.MatchString(*b.Namespace):
		problem(w, 400, "invalid_body", "namespace must be a slug or null")
		return
	case b.Role == "global_admin" && b.Namespace != nil:
		problem(w, 400, "invalid_body", "global_admin bindings must have a null namespace")
		return
	}
	b.ID = auth.NewID("rb")
	if _, err := s.db.Exec(`INSERT INTO role_binding(id,idp_group,namespace_id,role) VALUES(?,?,?,?)`, b.ID, b.IdpGroup, b.Namespace, b.Role); err != nil {
		problem(w, 500, "internal", "insert failed")
		return
	}
	s.audit(r, p, "role_binding.create", "role_binding", b.ID, "success", map[string]any{"idp_group": b.IdpGroup, "role": b.Role, "namespace": b.Namespace})
	writeJSON(w, 201, b)
}

func (s *server) deleteBinding(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.adminOnly(w, r, p, "role_binding.delete") {
		return
	}
	id := r.PathValue("id")
	res, _ := s.db.Exec(`DELETE FROM role_binding WHERE id=?`, id)
	if n, _ := res.RowsAffected(); n == 0 {
		s.audit(r, p, "role_binding.delete", "role_binding", id, "failed", nil)
		problem(w, 404, "not_found", "no such role binding")
		return
	}
	s.audit(r, p, "role_binding.delete", "role_binding", id, "success", nil)
	w.WriteHeader(204)
}
