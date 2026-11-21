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
	"github.com/KazzyAPI/control-8/internal/events"
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
		if authz.Effective(s.db, p, n.Slug) == 0 && !s.seesStackIn(p, n.ID) { // PRD-014
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
	var assets int // DOM-011 (D-64); since M17 also stacks and environments placed here
	s.db.QueryRow(`SELECT (SELECT count(*) FROM asset WHERE home_namespace_id=?1)+(SELECT count(*) FROM stack WHERE namespace_id=?1)+(SELECT count(*) FROM environment WHERE namespace_id=?1)`, n.ID).Scan(&assets)
	if assets > 0 {
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
	Namespace   *string `json:"namespace"` // legacy home namespace (PRD-002); null for applications created after M17
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Owner       *string `json:"owner"`
	ImageRepo   *string `json:"image_repo"`
	ManagedBy   string  `json:"managed_by"`
	ExternalURL *string `json:"external_url"`
	Product     string  `json:"product"`
	ExternalRef *string `json:"external_ref"`
	ProdID      string  `json:"-"`
}

const assetCols = `a.id,hn.slug,a.slug,a.name,a.description,a.owner,a.image_repo,a.managed_by,a.external_url,p.slug,a.external_ref,a.product_id`
const assetFrom = ` FROM asset a JOIN product p ON p.id=a.product_id LEFT JOIN namespace hn ON hn.id=a.home_namespace_id `

func scanAsset(row interface{ Scan(...any) error }) (a assetRow, err error) {
	err = row.Scan(&a.ID, &a.Namespace, &a.Slug, &a.Name, &a.Description, &a.Owner, &a.ImageRepo, &a.ManagedBy, &a.ExternalURL, &a.Product, &a.ExternalRef, &a.ProdID)
	return
}

// listAssets lists the applications with a stack in ns or legacy home ns, optionally one product (12 API table).
func (s *server) listAssets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "viewer", "asset.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	prod := r.URL.Query().Get("product")
	rows, err := s.db.Query(`SELECT `+assetCols+assetFrom+`WHERE (a.home_namespace_id=?1 OR EXISTS (SELECT 1 FROM stack st WHERE st.asset_id=a.id AND st.namespace_id=?1))
		AND (?2='' OR p.slug=?2) AND a.slug||'/'||p.slug>?3 ORDER BY a.slug||'/'||p.slug LIMIT ?4`, n.ID, prod, after, limit+1)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	defer rows.Close()
	items, next := []assetRow{}, (*string)(nil)
	for rows.Next() {
		a, _ := scanAsset(rows)
		if len(items) == limit {
			next = cursor(items[limit-1].Slug + "/" + items[limit-1].Product)
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
	ExternalRef *string `json:"external_ref"`
	Product     string  `json:"product"`
}

var managedByKinds = []string{"control8", "argo", "flux", "helm", "operator", "external"} // PRD-005

// validAsset: external_url must be absolute http(s) and external_ref/external_url only exist on register-only
// (non-control8) applications (02).
func validAsset(managedBy string, ext, ref *string) bool {
	if ref != nil && *ref != "" && managedBy == "control8" {
		return false
	}
	if ext == nil || *ext == "" {
		return true
	}
	u, err := url.Parse(*ext)
	return managedBy != "control8" && err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// createAsset is the deprecated namespaced create: product defaults to `unassigned`, whose apps are homed in ns.
func (s *server) createAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, ok := s.loadNS(w, r, p, "operator", "asset.create")
	if !ok {
		return
	}
	var in assetIn
	if !decode(w, r, &in) {
		return
	}
	if in.Product == "" {
		in.Product = "unassigned"
	}
	prod, err := s.productBySlug(in.Product)
	if err != nil {
		problem(w, 400, "invalid_body", "no such product")
		return
	}
	var home *nsRow
	if prod.Builtin {
		home = &n
	} else if role := s.prodRole(p, prod.ID); role < authz.Rank("operator") {
		s.denyNS(w, r, p, n.Slug, "asset.create", forbidOrNotFound(role))
		return
	}
	s.insertApp(w, r, p, prod, home, in)
}

func forbidOrNotFound(role int) error {
	if role == 0 {
		return authz.ErrNotFound
	}
	return authz.ErrForbidden
}

// insertApp validates and inserts an application, audits and publishes it (shared by both create routes).
func (s *server) insertApp(w http.ResponseWriter, r *http.Request, p *auth.Principal, prod prodRec, home *nsRow, in assetIn) {
	if in.ManagedBy == "" {
		in.ManagedBy = "control8"
	}
	hn, hnID := "", any(nil)
	if home != nil {
		hn, hnID = home.Slug, home.ID
	}
	if !slugRe.MatchString(in.Slug) || in.Name == nil || strings.TrimSpace(*in.Name) == "" ||
		!slices.Contains(managedByKinds, in.ManagedBy) || !validAsset(in.ManagedBy, in.ExternalURL, in.ExternalRef) {
		problem(w, 400, "invalid_body", "valid slug, name, managed_by and (register-only apps) external_url/external_ref required")
		return
	}
	id, now := auth.NewID("ast"), store.Ts(s.now())
	_, err := s.db.Exec(`INSERT INTO asset(id,product_id,home_namespace_id,slug,name,description,owner,image_repo,managed_by,external_ref,external_url,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, prod.ID, hnID, in.Slug, *in.Name, in.Description, in.Owner, in.ImageRepo, in.ManagedBy, in.ExternalRef, in.ExternalURL, now, now)
	if err != nil {
		s.auditNS(r, p, hn, "asset.create", "asset", in.Slug, "failed", map[string]any{"product": prod.Slug})
		problem(w, 409, "asset_exists", "asset already exists")
		return
	}
	s.auditNS(r, p, hn, "asset.create", "asset", id, "success", map[string]any{"slug": in.Slug, "managed_by": in.ManagedBy, "product": prod.Slug})
	a, _ := scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.id=?`, id))
	s.publishApp(a, false)
	writeJSON(w, 201, a)
}

// publishApp emits application.updated (12 Events).
func (s *server) publishApp(a assetRow, deleted bool) {
	var data any = a
	if deleted {
		data = map[string]any{"id": a.ID, "slug": a.Slug, "product": a.Product, "deleted": true}
	}
	s.publish(events.Event{Type: "application.updated", At: s.now(), Product: a.ProdID, Data: data})
}

// appRole is the effective role on an application (PRD-011): global + product bindings; for legacy apps of the
// built-in `unassigned` product also the home namespace role, so the M2 behaviour is unchanged.
func (s *server) appRole(p *auth.Principal, a assetRow) int {
	r := s.prodRole(p, a.ProdID)
	if a.Product == "unassigned" && a.Namespace != nil {
		r = max(r, authz.Effective(s.db, p, *a.Namespace))
	}
	return r
}

// loadAsset is the namespaced lookup: the caller needs a namespace viewer role (as before M17); mutations need
// appRole >= min. The slug must resolve to one application in ns (optionally `?product=`), else 400 asset_ambiguous.
func (s *server) loadAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (assetRow, bool) {
	n, ok := s.loadNS(w, r, p, "viewer", action)
	if !ok {
		return assetRow{}, false
	}
	rows, err := s.db.Query(`SELECT `+assetCols+assetFrom+`WHERE a.slug=?2 AND (?3='' OR p.slug=?3) AND
		(a.home_namespace_id=?1 OR EXISTS (SELECT 1 FROM stack st WHERE st.asset_id=a.id AND st.namespace_id=?1))`, n.ID, r.PathValue("asset"), r.URL.Query().Get("product"))
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return assetRow{}, false
	}
	var found []assetRow
	for rows.Next() {
		a, _ := scanAsset(rows)
		found = append(found, a)
	}
	rows.Close()
	switch {
	case len(found) == 0:
		problem(w, 404, "not_found", "not found")
		return assetRow{}, false
	case len(found) > 1:
		problem(w, 400, "asset_ambiguous", "several applications share this slug here; add ?product=")
		return assetRow{}, false
	}
	a := found[0]
	if min != "viewer" && s.appRole(p, a) < authz.Rank(min) {
		s.denyNS(w, r, p, n.Slug, action, authz.ErrForbidden)
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
	if ok {
		s.patchApp(w, r, p, a)
	}
}

// patchApp updates an application; `product` reassigns it (PRD-004, global_admin only).
func (s *server) patchApp(w http.ResponseWriter, r *http.Request, p *auth.Principal, a assetRow) {
	var in assetIn
	if !decode(w, r, &in) {
		return
	}
	hn := ptrStr(a.Namespace)
	if in.Slug != "" || in.ManagedBy != "" || in.Name != nil && strings.TrimSpace(*in.Name) == "" || !validAsset(a.ManagedBy, in.ExternalURL, in.ExternalRef) {
		problem(w, 400, "invalid_body", "slug and managed_by are immutable; name must not be empty; external_url/external_ref are for register-only apps")
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
	var target *prodRec
	if in.Product != "" && in.Product != a.Product {
		if authz.Effective(s.db, p, "") != 4 {
			s.auditNS(r, p, hn, "application.reassign", "asset", a.ID, "denied", map[string]any{"from": a.Product, "to": in.Product})
			problem(w, 403, "forbidden", "only global_admin reassigns an application")
			return
		}
		t, err := s.productBySlug(in.Product)
		if err != nil {
			problem(w, 400, "invalid_body", "no such product")
			return
		}
		var dup int
		s.db.QueryRow(`SELECT count(*) FROM asset WHERE product_id=? AND slug=?`, t.ID, a.Slug).Scan(&dup)
		if dup > 0 {
			problem(w, 409, "asset_exists", "the target product already has an application with this slug")
			return
		}
		rows, _ := s.db.Query(`SELECT n.id FROM stack st JOIN namespace n ON n.id=st.namespace_id WHERE st.asset_id=? AND st.state!='removed'`, a.ID)
		var nsIDs []string
		for rows != nil && rows.Next() {
			var id string
			rows.Scan(&id)
			nsIDs = append(nsIDs, id)
		}
		if rows != nil {
			rows.Close()
		}
		for _, id := range nsIDs { // POL-R15 still holds for stacks that follow the app
			if rules, err := s.rules(id); err != nil || !rules.ProductAllowed(t.Slug) {
				s.auditNS(r, p, hn, "application.reassign", "asset", a.ID, "denied", map[string]any{"reason": "product_not_allowed_in_namespace", "to": t.Slug})
				problem(w, 403, "product_not_allowed_in_namespace", "a namespace with a stack of this application does not allow the target product")
				return
			}
		}
		target = &t
	}
	tx, err := s.db.Begin()
	if err != nil {
		problem(w, 500, "internal", "begin failed")
		return
	}
	defer tx.Rollback()
	now := store.Ts(s.now())
	tx.Exec(`UPDATE asset SET name=?,description=?,owner=?,image_repo=?,external_url=?,external_ref=?,updated_at=? WHERE id=?`, name,
		pick(in.Description, a.Description), pick(in.Owner, a.Owner), pick(in.ImageRepo, a.ImageRepo), pick(in.ExternalURL, a.ExternalURL), pick(in.ExternalRef, a.ExternalRef), now, a.ID)
	if target != nil { // stacks follow the application; their environment belonged to the old product, so they become legacy placements
		tx.Exec(`UPDATE asset SET product_id=?,home_namespace_id=NULL WHERE id=?`, target.ID, a.ID)
		tx.Exec(`UPDATE stack SET environment_id=NULL,updated_at=? WHERE asset_id=?`, now, a.ID)
	}
	if tx.Commit() != nil {
		problem(w, 500, "internal", "commit failed")
		return
	}
	s.auditNS(r, p, hn, "asset.update", "asset", a.ID, "success", map[string]any{"product": a.Product})
	if target != nil {
		s.auditNS(r, p, hn, "application.reassign", "asset", a.ID, "success", map[string]any{"from": a.Product, "to": target.Slug})
		s.publishStacksOf(a.ID)
	}
	a, _ = scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.id=?`, a.ID))
	s.publishApp(a, false)
	writeJSON(w, 200, a)
}

// publishStacksOf re-publishes an application's stacks (their product/environment changed).
func (s *server) publishStacksOf(assetID string) {
	rows, err := s.db.Query(stackSel+`WHERE st.asset_id=? AND st.state!='removed'`, assetID)
	if err != nil {
		return
	}
	var all []stackRec
	for rows.Next() {
		x, _ := scanStack(rows)
		all = append(all, x)
	}
	rows.Close()
	for _, x := range all {
		s.publishStack(x)
	}
}

func (s *server) deleteAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if a, ok := s.loadAsset(w, r, p, "admin", "asset.delete"); ok {
		s.removeApp(w, r, p, a)
	}
}

func (s *server) removeApp(w http.ResponseWriter, r *http.Request, p *auth.Principal, a assetRow) {
	hn := ptrStr(a.Namespace)
	var stacks int
	s.db.QueryRow(`SELECT count(*) FROM stack WHERE asset_id=?`, a.ID).Scan(&stacks)
	if stacks > 0 { // removed stacks keep their row, so they count too
		s.auditNS(r, p, hn, "asset.delete", "asset", a.ID, "failed", map[string]any{"product": a.Product})
		problem(w, 409, "asset_has_stacks", "asset still has stacks")
		return
	}
	s.db.Exec(`DELETE FROM asset WHERE id=?`, a.ID)
	s.auditNS(r, p, hn, "asset.delete", "asset", a.ID, "success", map[string]any{"product": a.Product})
	s.publishApp(a, true)
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
