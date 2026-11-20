package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/store"
)

func (s *server) nsRoutes(reg func(string, http.HandlerFunc)) {
	reg("GET /api/v1/namespaces", s.api(s.listNamespaces))
	reg("POST /api/v1/namespaces", s.api(s.createNamespace))
	reg("GET /api/v1/namespaces/{ns}", s.api(s.getNamespace))
	reg("PATCH /api/v1/namespaces/{ns}", s.api(s.updateNamespace))
	reg("DELETE /api/v1/namespaces/{ns}", s.api(s.deleteNamespace))
	reg("GET /api/v1/namespaces/{ns}/assets", s.api(s.listAssets))
	reg("POST /api/v1/namespaces/{ns}/assets", s.api(s.createAsset))
	reg("GET /api/v1/namespaces/{ns}/assets/{asset}", s.api(s.getAsset))
	reg("PATCH /api/v1/namespaces/{ns}/assets/{asset}", s.api(s.updateAsset))
	reg("DELETE /api/v1/namespaces/{ns}/assets/{asset}", s.api(s.deleteAsset))
	reg("GET /api/v1/audit", s.api(s.queryAudit))
}

// page parses ?limit= (default 50, max 200) and the opaque ?cursor= (API-006).
func page(w http.ResponseWriter, r *http.Request) (limit int, after string, ok bool) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problem(w, 400, "invalid_query", "limit must be 1..200")
			return
		}
		limit = n
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problem(w, 400, "invalid_query", "bad cursor")
			return
		}
		after = string(b)
	}
	return limit, after, true
}

func cursor(v string) *string {
	c := base64.RawURLEncoding.EncodeToString([]byte(v))
	return &c
}

type nsRow struct {
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Display   string  `json:"display_name"`
	Tier      string  `json:"environment_tier"`
	Approver  *string `json:"approver_group"`
	Desc      *string `json:"description"`
	CreatedAt string  `json:"created_at"`
}

const nsCols = `id,slug,display_name,environment_tier,approver_group,description,created_at`

func scanNS(row interface{ Scan(...any) error }) (n nsRow, err error) {
	err = row.Scan(&n.ID, &n.Slug, &n.Display, &n.Tier, &n.Approver, &n.Desc, &n.CreatedAt)
	return
}

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *server) listNamespaces(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := s.db.Query(`SELECT `+nsCols+` FROM namespace WHERE slug>? ORDER BY slug`, after)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	var all []nsRow
	for rows.Next() {
		n, _ := scanNS(rows)
		all = append(all, n)
	}
	rows.Close()
	items, next := []nsRow{}, (*string)(nil)
	for _, n := range all {
		if authz.Effective(s.db, p, n.Slug) == 0 {
			continue
		}
		if len(items) == limit {
			next = cursor(items[len(items)-1].Slug)
			break
		}
		items = append(items, n)
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

func (s *server) createNamespace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.deny(w, r, p, "namespace.create", err)
		return
	}
	var in struct {
		Slug            string `json:"slug"`
		DisplayName     string `json:"display_name"`
		EnvironmentTier string `json:"environment_tier"`
		ApproverGroup   string `json:"approver_group"`
		Description     string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case !slugRe.MatchString(in.Slug) || strings.TrimSpace(in.DisplayName) == "":
		problem(w, 400, "invalid_body", "slug and display_name are required")
		return
	case in.EnvironmentTier != "prod" && in.EnvironmentTier != "non-prod": // GRT-001
		problem(w, 400, "invalid_body", "environment_tier must be prod or non-prod")
		return
	case in.EnvironmentTier == "prod" && in.ApproverGroup == "": // GRT-027
		problem(w, 400, "approver_group_missing", "prod namespaces require approver_group")
		return
	}
	id, now := auth.NewID("ns"), store.Ts(s.now())
	_, err := s.db.Exec(`INSERT INTO namespace(id,slug,display_name,environment_tier,approver_group,description,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		id, in.Slug, in.DisplayName, in.EnvironmentTier, emptyNil(in.ApproverGroup), emptyNil(in.Description), now, now)
	if err != nil {
		s.auditNS(r, p, in.Slug, "namespace.create", "namespace", in.Slug, "failed", nil)
		problem(w, 409, "namespace_exists", "namespace already exists")
		return
	}
	s.auditNS(r, p, in.Slug, "namespace.create", "namespace", id, "success", map[string]any{"environment_tier": in.EnvironmentTier, "approver_group": emptyNil(in.ApproverGroup)})
	n, _ := scanNS(s.db.QueryRow(`SELECT `+nsCols+` FROM namespace WHERE id=?`, id))
	writeJSON(w, 201, n)
}

// loadNS authorizes (404 if not viewable, 403 if role too low; denials audited) then loads the namespace row.
func (s *server) loadNS(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (nsRow, bool) {
	slug := r.PathValue("ns")
	if err := authz.Require(s.db, p, slug, min); err != nil {
		s.denyNS(w, r, p, slug, action, err)
		return nsRow{}, false
	}
	n, err := scanNS(s.db.QueryRow(`SELECT `+nsCols+` FROM namespace WHERE slug=?`, slug))
	if err != nil {
		problem(w, 404, "not_found", "not found")
		return n, false
	}
	return n, true
}

func (s *server) getNamespace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if n, ok := s.loadNS(w, r, p, "viewer", "namespace.get"); ok {
		writeJSON(w, 200, n)
	}
}

func (s *server) updateNamespace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "admin", "namespace.update")
	if !ok {
		return
	}
	var in struct {
		DisplayName     *string `json:"display_name"`
		Description     *string `json:"description"`
		EnvironmentTier *string `json:"environment_tier"`
		ApproverGroup   *string `json:"approver_group"`
	}
	if !decode(w, r, &in) {
		return
	}
	if (in.EnvironmentTier != nil || in.ApproverGroup != nil) && authz.Effective(s.db, p, "") != 4 { // D-50, GRT-003
		s.auditNS(r, p, n.Slug, "namespace.update", "namespace", n.ID, "denied", map[string]any{"reason": "tier_or_approver_group"})
		problem(w, 403, "forbidden", "only global_admin changes environment_tier or approver_group")
		return
	}
	tier, group := n.Tier, ""
	if n.Approver != nil {
		group = *n.Approver
	}
	if in.EnvironmentTier != nil {
		tier = *in.EnvironmentTier
	}
	if in.ApproverGroup != nil {
		group = *in.ApproverGroup
	}
	switch {
	case tier != "prod" && tier != "non-prod":
		problem(w, 400, "invalid_body", "environment_tier must be prod or non-prod")
		return
	case in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) == "":
		problem(w, 400, "invalid_body", "display_name must not be empty")
		return
	case tier == "prod" && group == "":
		problem(w, 400, "approver_group_missing", "prod namespaces require approver_group")
		return
	}
	display, desc := n.Display, n.Desc
	if in.DisplayName != nil {
		display = *in.DisplayName
	}
	if in.Description != nil {
		desc = in.Description
	}
	s.db.Exec(`UPDATE namespace SET display_name=?,description=?,environment_tier=?,approver_group=?,updated_at=? WHERE id=?`,
		display, desc, tier, emptyNil(group), store.Ts(s.now()), n.ID)
	d := map[string]any{"from_tier": n.Tier, "to_tier": tier, "approver_group": emptyNil(group)}
	if tier != n.Tier {
		s.auditNS(r, p, n.Slug, "namespace.tier_change", "namespace", n.ID, "success", d) // GRT-003
	}
	s.auditNS(r, p, n.Slug, "namespace.update", "namespace", n.ID, "success", d)
	n, _ = scanNS(s.db.QueryRow(`SELECT `+nsCols+` FROM namespace WHERE id=?`, n.ID))
	writeJSON(w, 200, n)
}

func (s *server) deleteNamespace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	slug := r.PathValue("ns")
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.denyNS(w, r, p, slug, "namespace.delete", err)
		return
	}
	n, err := scanNS(s.db.QueryRow(`SELECT `+nsCols+` FROM namespace WHERE slug=?`, slug))
	if err != nil {
		s.auditNS(r, p, slug, "namespace.delete", "namespace", slug, "failed", nil)
		problem(w, 404, "not_found", "not found")
		return
	}
	var assets int
	s.db.QueryRow(`SELECT count(*) FROM asset WHERE namespace_id=?`, n.ID).Scan(&assets)
	if assets > 0 { // DOM-011 (D-64)
		s.auditNS(r, p, slug, "namespace.delete", "namespace", n.ID, "failed", nil)
		problem(w, 409, "namespace_not_empty", "namespace still has assets")
		return
	}
	s.db.Exec(`DELETE FROM policy WHERE namespace_id=?`, n.ID)
	s.db.Exec(`DELETE FROM namespace WHERE id=?`, n.ID)
	s.auditNS(r, p, slug, "namespace.delete", "namespace", n.ID, "success", nil)
	w.WriteHeader(204)
}

type assetRow struct {
	ID          string  `json:"id"`
	Namespace   string  `json:"namespace"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Owner       *string `json:"owner"`
	ImageRepo   *string `json:"image_repo"`
	ManagedBy   string  `json:"managed_by"`
	ExternalURL *string `json:"external_url"`
}

const assetCols = `a.id,n.slug,a.slug,a.name,a.description,a.owner,a.image_repo,a.managed_by,a.external_url`
const assetFrom = ` FROM asset a JOIN namespace n ON n.id=a.namespace_id `

func scanAsset(row interface{ Scan(...any) error }) (a assetRow, err error) {
	err = row.Scan(&a.ID, &a.Namespace, &a.Slug, &a.Name, &a.Description, &a.Owner, &a.ImageRepo, &a.ManagedBy, &a.ExternalURL)
	return
}

func (s *server) listAssets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "viewer", "asset.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	rows, err := s.db.Query(`SELECT `+assetCols+assetFrom+`WHERE a.namespace_id=? AND a.slug>? ORDER BY a.slug LIMIT ?`, n.ID, after, limit+1)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []assetRow{}, (*string)(nil)
	for rows.Next() {
		a, _ := scanAsset(rows)
		if len(items) == limit {
			next = cursor(items[limit-1].Slug)
			break
		}
		items = append(items, a)
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

type assetIn struct {
	Slug        string  `json:"slug"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Owner       *string `json:"owner"`
	ImageRepo   *string `json:"image_repo"`
	ManagedBy   string  `json:"managed_by"`
	ExternalURL *string `json:"external_url"`
}

// validAsset: external_url must be absolute http(s) and only exists on Argo assets (02).
func validAsset(managedBy string, ext *string) bool {
	if ext == nil || *ext == "" {
		return true
	}
	u, err := url.Parse(*ext)
	return managedBy == "argo" && err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *server) createAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "operator", "asset.create")
	if !ok {
		return
	}
	var in assetIn
	if !decode(w, r, &in) {
		return
	}
	if in.ManagedBy == "" {
		in.ManagedBy = "control8"
	}
	if !slugRe.MatchString(in.Slug) || in.Name == nil || strings.TrimSpace(*in.Name) == "" ||
		(in.ManagedBy != "control8" && in.ManagedBy != "argo") || !validAsset(in.ManagedBy, in.ExternalURL) {
		problem(w, 400, "invalid_body", "valid slug, name, managed_by and (argo only) external_url required")
		return
	}
	id, now := auth.NewID("ast"), store.Ts(s.now())
	_, err := s.db.Exec(`INSERT INTO asset(id,namespace_id,slug,name,description,owner,image_repo,managed_by,external_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, n.ID, in.Slug, *in.Name, in.Description, in.Owner, in.ImageRepo, in.ManagedBy, in.ExternalURL, now, now)
	if err != nil {
		s.auditNS(r, p, n.Slug, "asset.create", "asset", in.Slug, "failed", nil)
		problem(w, 409, "asset_exists", "asset already exists")
		return
	}
	s.auditNS(r, p, n.Slug, "asset.create", "asset", id, "success", map[string]any{"slug": in.Slug, "managed_by": in.ManagedBy})
	a, _ := scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.id=?`, id))
	writeJSON(w, 201, a)
}

func (s *server) loadAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (assetRow, bool) {
	n, ok := s.loadNS(w, r, p, min, action)
	if !ok {
		return assetRow{}, false
	}
	a, err := scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.namespace_id=? AND a.slug=?`, n.ID, r.PathValue("asset")))
	if err != nil {
		problem(w, 404, "not_found", "not found")
		return a, false
	}
	return a, true
}

func (s *server) getAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if a, ok := s.loadAsset(w, r, p, "viewer", "asset.get"); ok {
		writeJSON(w, 200, a)
	}
}

func (s *server) updateAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	a, ok := s.loadAsset(w, r, p, "operator", "asset.update")
	if !ok {
		return
	}
	var in assetIn
	if !decode(w, r, &in) {
		return
	}
	if in.Slug != "" || in.ManagedBy != "" || in.Name != nil && strings.TrimSpace(*in.Name) == "" || !validAsset(a.ManagedBy, in.ExternalURL) {
		problem(w, 400, "invalid_body", "slug and managed_by are immutable; name must not be empty; external_url is for argo assets")
		return
	}
	pick := func(n, old *string) *string {
		if n != nil {
			return n
		}
		return old
	}
	name := a.Name
	if in.Name != nil {
		name = *in.Name
	}
	s.db.Exec(`UPDATE asset SET name=?,description=?,owner=?,image_repo=?,external_url=?,updated_at=? WHERE id=?`, name,
		pick(in.Description, a.Description), pick(in.Owner, a.Owner), pick(in.ImageRepo, a.ImageRepo), pick(in.ExternalURL, a.ExternalURL), store.Ts(s.now()), a.ID)
	s.auditNS(r, p, a.Namespace, "asset.update", "asset", a.ID, "success", nil)
	a, _ = scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.id=?`, a.ID))
	writeJSON(w, 200, a)
}

func (s *server) deleteAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	a, ok := s.loadAsset(w, r, p, "admin", "asset.delete")
	if !ok {
		return
	}
	var stacks int
	s.db.QueryRow(`SELECT count(*) FROM stack WHERE asset_id=?`, a.ID).Scan(&stacks)
	if stacks > 0 { // removed stacks keep their row, so they count too
		s.auditNS(r, p, a.Namespace, "asset.delete", "asset", a.ID, "failed", nil)
		problem(w, 409, "asset_has_stacks", "asset still has stacks")
		return
	}
	s.db.Exec(`DELETE FROM asset WHERE id=?`, a.ID)
	s.auditNS(r, p, a.Namespace, "asset.delete", "asset", a.ID, "success", nil)
	w.WriteHeader(204)
}

// queryAudit: viewers see their namespaces' events, global (null-namespace) events only global_admin.
func (s *server) queryAudit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	global := authz.Effective(s.db, p, "") == 4
	where, args := []string{"1=1"}, []any{}
	if ns := q.Get("namespace"); ns != "" {
		if err := authz.Require(s.db, p, ns, "viewer"); err != nil {
			s.denyNS(w, r, p, ns, "audit.query", err)
			return
		}
		where, args = append(where, "namespace_id=?"), append(args, ns)
	} else if !global {
		var slugs []any
		rows, _ := s.db.Query(`SELECT slug FROM namespace`)
		for rows != nil && rows.Next() {
			var sl string
			rows.Scan(&sl)
			slugs = append(slugs, sl)
		}
		if rows != nil {
			rows.Close()
		}
		slugs = slices.DeleteFunc(slugs, func(sl any) bool { return authz.Effective(s.db, p, sl.(string)) == 0 })
		if len(slugs) == 0 {
			s.deny(w, r, p, "audit.query", authz.ErrNotFound)
			return
		}
		where, args = append(where, "namespace_id IN (?"+strings.Repeat(",?", len(slugs)-1)+")"), append(args, slugs...)
	}
	for col, key := range map[string]string{"action": "action", "target_id": "target_id", "actor_id": "actor_id"} {
		if v := q.Get(key); v != "" {
			where, args = append(where, col+"=?"), append(args, v)
		}
	}
	if v := q.Get("grant_id"); v != "" {
		where, args = append(where, "json_extract(details,'$.grant_id')=?"), append(args, v)
	}
	if v := q.Get("since"); v != "" {
		where, args = append(where, "at>=?"), append(args, v)
	}
	if v := q.Get("until"); v != "" {
		where, args = append(where, "at<=?"), append(args, v)
	}
	if after != "" {
		id, err := strconv.Atoi(after)
		if err != nil {
			problem(w, 400, "invalid_query", "bad cursor")
			return
		}
		where, args = append(where, "id<?"), append(args, id)
	}
	rows, err := s.db.Query(`SELECT id,at,actor_type,actor_id,actor_display,auth_method,namespace_id,action,target_type,target_id,outcome,details,request_id
		FROM audit_event WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []map[string]any{}, (*string)(nil)
	for rows.Next() {
		var id int
		var at, atype, action, outcome, details string
		var aid, adisp, method, ns, ttype, tid, req sql.NullString
		rows.Scan(&id, &at, &atype, &aid, &adisp, &method, &ns, &action, &ttype, &tid, &outcome, &details, &req)
		if len(items) == limit {
			next = cursor(strconv.Itoa(items[limit-1]["id"].(int)))
			break
		}
		nv := func(x sql.NullString) any {
			if x.Valid {
				return x.String
			}
			return nil
		}
		items = append(items, map[string]any{"id": id, "at": at, "actor_type": atype, "actor_id": nv(aid), "actor_display": nv(adisp),
			"auth_method": nv(method), "namespace": nv(ns), "action": action, "target_type": nv(ttype), "target_id": nv(tid),
			"outcome": outcome, "details": json.RawMessage(details), "request_id": nv(req)})
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
