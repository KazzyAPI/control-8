package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/store"
)

func (s *server) productRoutes(reg func(string, http.HandlerFunc)) {
	const pr = "/api/v1/products/{product}"
	reg("GET /api/v1/products", s.api(s.listProducts))
	reg("POST /api/v1/products", s.api(s.createProduct))
	reg("GET "+pr, s.api(s.getProduct))
	reg("PATCH "+pr, s.api(s.updateProduct))
	reg("DELETE "+pr, s.api(s.deleteProduct))
	reg("GET "+pr+"/applications", s.api(s.listApplications))
	reg("POST "+pr+"/applications", s.api(s.createApplication))
	reg("GET "+pr+"/applications/{app}", s.api(s.getApplication))
	reg("PATCH "+pr+"/applications/{app}", s.api(s.updateApplication))
	reg("DELETE "+pr+"/applications/{app}", s.api(s.deleteApplication))
	reg("POST "+pr+"/applications/{app}/stacks", s.api(s.placeApplication))
	reg("GET "+pr+"/environments", s.api(s.listEnvironments))
	reg("POST "+pr+"/environments", s.api(s.createEnvironment))
	reg("GET "+pr+"/environments/{env}", s.api(s.getEnvironment))
	reg("PATCH "+pr+"/environments/{env}", s.api(s.updateEnvironment))
	reg("DELETE "+pr+"/environments/{env}", s.api(s.deleteEnvironment))
	reg("GET "+pr+"/overview", s.api(s.productOverview))
}

type prodRec struct {
	ID, Slug, Name string
	Desc, Owner    *string
	Group          *string
	Links, Tags    string
	Builtin        bool
	CreatedAt, Upd string
}

const prodCols = `id,slug,name,description,owner,approver_group,links,tags,builtin,created_at,updated_at`

func scanProd(row interface{ Scan(...any) error }) (x prodRec, err error) {
	err = row.Scan(&x.ID, &x.Slug, &x.Name, &x.Desc, &x.Owner, &x.Group, &x.Links, &x.Tags, &x.Builtin, &x.CreatedAt, &x.Upd)
	return
}

func (s *server) productBySlug(slug string) (prodRec, error) {
	return scanProd(s.db.QueryRow(`SELECT `+prodCols+` FROM product WHERE slug=?`, slug))
}

// base is the Product shape without caller-specific counts (also the event payload).
func (x prodRec) base() map[string]any {
	return map[string]any{"id": x.ID, "slug": x.Slug, "name": x.Name, "description": x.Desc, "owner": x.Owner,
		"links": json.RawMessage(x.Links), "approver_group": x.Group, "tags": json.RawMessage(x.Tags), "builtin": x.Builtin}
}

func (s *server) publishProduct(x prodRec) {
	s.publish(events.Event{Type: "product.updated", At: s.now(), Product: x.ID, Data: x.base()})
}

// prodRole is the effective role on a product or application: global + product bindings (PRD-011).
func (s *server) prodRole(p *auth.Principal, prodID string) int {
	return authz.EffectiveIn(s.db, p, "", prodID)
}

// productNamespaces lists the namespaces where a product has stacks or legacy home applications.
func (s *server) productNamespaces(prodID string) []string {
	rows, err := s.db.Query(`SELECT n.slug FROM stack st JOIN asset a ON a.id=st.asset_id JOIN namespace n ON n.id=st.namespace_id WHERE a.product_id=?1 AND st.state!='removed'
		UNION SELECT n.slug FROM asset a JOIN namespace n ON n.id=a.home_namespace_id WHERE a.product_id=?1`, prodID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

// canSeeProduct: a global/product role, or a namespace role on a namespace where the product has stacks (PRD-014).
func (s *server) canSeeProduct(p *auth.Principal, prodID string) bool {
	if prodID == "" {
		return false
	}
	if s.prodRole(p, prodID) > 0 {
		return true
	}
	return slices.ContainsFunc(s.productNamespaces(prodID), func(n string) bool { return authz.Effective(s.db, p, n) > 0 })
}

// seesStackIn: the caller can see at least one stack in the namespace through a product role (PRD-014).
func (s *server) seesStackIn(p *auth.Principal, nsID string) bool {
	rows, err := s.db.Query(`SELECT DISTINCT a.product_id FROM stack st JOIN asset a ON a.id=st.asset_id WHERE st.namespace_id=? AND st.state!='removed'`, nsID)
	if err != nil {
		return false
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	return slices.ContainsFunc(ids, func(id string) bool { return s.prodRole(p, id) > 0 })
}

// loadProduct resolves {product}: viewing needs any way to see it (404 otherwise), other minimums need that role
// on the product itself (global or product binding; 403).
func (s *server) loadProduct(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (prodRec, bool) {
	x, err := s.productBySlug(r.PathValue("product"))
	if err != nil {
		s.deny(w, r, p, action, authz.ErrNotFound)
		return x, false
	}
	role := s.prodRole(p, x.ID)
	switch {
	case min == "viewer" && s.canSeeProduct(p, x.ID):
		return x, true
	case role >= authz.Rank(min):
		return x, true
	}
	e := authz.ErrForbidden
	if !s.canSeeProduct(p, x.ID) {
		e = authz.ErrNotFound
	}
	s.audit(r, p, action, "product", x.ID, "denied", map[string]any{"product": x.Slug})
	if e == authz.ErrNotFound {
		problem(w, 404, "not_found", "not found")
	} else {
		problem(w, 403, "forbidden", "insufficient role")
	}
	return x, false
}

// visibleStacks returns the product's non-removed stacks the caller may see (PRD-030).
func (s *server) visibleStacks(p *auth.Principal, prodID string) []stackRec {
	rows, err := s.db.Query(stackSel+`WHERE a.product_id=? AND st.state!='removed' ORDER BY a.slug,n.slug,st.slug`, prodID)
	if err != nil {
		return nil
	}
	var all []stackRec
	for rows.Next() {
		x, _ := scanStack(rows)
		all = append(all, x)
	}
	rows.Close()
	return slices.DeleteFunc(all, func(x stackRec) bool { return authz.EffectiveIn(s.db, p, x.NS, x.ProdID) == 0 })
}

func (s *server) productJSON(p *auth.Principal, x prodRec) map[string]any {
	m := x.base()
	stacks := s.visibleStacks(p, x.ID)
	c := map[string]int{"applications": 0, "stacks": len(stacks), "prod": 0, "non_prod": 0, "unhealthy": 0, "open_findings": 0, "active_grants": 0}
	for _, st := range stacks {
		if st.Tier == "prod" {
			c["prod"]++
		} else {
			c["non_prod"]++
		}
		if st.Health == "degraded" || st.Health == "drifted" {
			c["unhealthy"]++
		}
	}
	for _, a := range s.visibleApps(p, x.ID, "", 0) {
		_ = a
		c["applications"]++
	}
	now, ag := store.Ts(s.now()), 0
	s.db.QueryRow(`SELECT count(*) FROM access_grant WHERE product_id=? AND status IN ('approved','active') AND starts_at<=? AND expires_at>?`, x.ID, now, now).Scan(&ag)
	c["active_grants"] = ag
	m["counts"] = c
	return m
}

func (s *server) listProducts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	tag := r.URL.Query().Get("tag")
	rows, err := s.db.Query(`SELECT `+prodCols+` FROM product WHERE slug>? AND (?='' OR EXISTS (SELECT 1 FROM json_each(tags) WHERE value=?)) ORDER BY slug`, after, tag, tag)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	var all []prodRec
	for rows.Next() {
		x, _ := scanProd(rows)
		all = append(all, x)
	}
	rows.Close()
	items, next := []map[string]any{}, (*string)(nil)
	for _, x := range all {
		if !s.canSeeProduct(p, x.ID) {
			continue
		}
		if len(items) == limit {
			next = cursor(items[len(items)-1]["slug"].(string))
			break
		}
		items = append(items, s.productJSON(p, x))
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// validProductFields checks tags (≤ 20, slug-like) and links (label + absolute http(s) URL).
func validProductFields(tags []string, links []link) bool {
	if len(tags) > 20 || slices.ContainsFunc(tags, func(t string) bool { return !slugRe.MatchString(t) }) {
		return false
	}
	return !slices.ContainsFunc(links, func(l link) bool {
		u, err := url.Parse(l.URL)
		return strings.TrimSpace(l.Label) == "" || err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https"
	})
}

func (s *server) createProduct(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.deny(w, r, p, "product.create", err)
		return
	}
	var in struct {
		Slug, Name, Description, Owner string
		ApproverGroup                  string   `json:"approver_group"`
		Links                          []link   `json:"links"`
		Tags                           []string `json:"tags"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case !slugRe.MatchString(in.Slug) || strings.TrimSpace(in.Name) == "" || !validProductFields(in.Tags, in.Links):
		problem(w, 400, "invalid_body", "slug and name are required; tags are at most 20 slugs; links need label and http(s) url")
		return
	case strings.TrimSpace(in.ApproverGroup) == "": // PRD-001: a real product always has an approver group
		problem(w, 400, "approver_group_missing", "approver_group is required")
		return
	}
	if in.Links == nil {
		in.Links = []link{}
	}
	if in.Tags == nil {
		in.Tags = []string{}
	}
	id, now := auth.NewID("prd"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO product(id,slug,name,description,owner,links,approver_group,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, in.Slug, in.Name, emptyNil(in.Description), emptyNil(in.Owner), mj(in.Links), in.ApproverGroup, mj(in.Tags), now, now); err != nil {
		s.audit(r, p, "product.create", "product", in.Slug, "failed", nil)
		problem(w, 409, "product_exists", "product already exists")
		return
	}
	s.audit(r, p, "product.create", "product", id, "success", map[string]any{"slug": in.Slug, "approver_group": in.ApproverGroup})
	x, _ := s.productBySlug(in.Slug)
	s.publishProduct(x)
	writeJSON(w, 201, s.productJSON(p, x))
}

func (s *server) getProduct(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if x, ok := s.loadProduct(w, r, p, "viewer", "product.get"); ok {
		writeJSON(w, 200, s.productJSON(p, x))
	}
}

func (s *server) updateProduct(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "admin", "product.update")
	if !ok {
		return
	}
	var in struct {
		Name          *string   `json:"name"`
		Description   *string   `json:"description"`
		Owner         *string   `json:"owner"`
		Links         *[]link   `json:"links"`
		Tags          *[]string `json:"tags"`
		ApproverGroup *string   `json:"approver_group"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ApproverGroup != nil && authz.Effective(s.db, p, "") != 4 { // PRD-013, D-50
		s.audit(r, p, "product.update", "product", x.ID, "denied", map[string]any{"reason": "approver_group", "product": x.Slug})
		problem(w, 403, "forbidden", "only global_admin changes approver_group")
		return
	}
	var links []link
	var tags []string
	if in.Links != nil {
		links = *in.Links
	}
	if in.Tags != nil {
		tags = *in.Tags
	}
	switch {
	case in.Name != nil && (strings.TrimSpace(*in.Name) == "" || x.Builtin): // built-in cannot be renamed (PRD-003)
		problem(w, 400, "invalid_body", "name must not be empty and the built-in product cannot be renamed")
		return
	case in.ApproverGroup != nil && (strings.TrimSpace(*in.ApproverGroup) == "") != x.Builtin:
		problem(w, 400, "approver_group_missing", "a real product needs an approver_group; the built-in unassigned product has none (PRD-001)")
		return
	case !validProductFields(tags, links):
		problem(w, 400, "invalid_body", "tags are at most 20 slugs; links need label and http(s) url")
		return
	}
	name, desc, owner, group, ls, ts := x.Name, x.Desc, x.Owner, x.Group, x.Links, x.Tags
	if in.Name != nil {
		name = *in.Name
	}
	if in.Description != nil {
		desc = in.Description
	}
	if in.Owner != nil {
		owner = in.Owner
	}
	if in.ApproverGroup != nil && !x.Builtin {
		group = in.ApproverGroup
	}
	if in.Links != nil {
		ls = mj(links)
	}
	if in.Tags != nil {
		ts = mj(tags)
	}
	s.db.Exec(`UPDATE product SET name=?,description=?,owner=?,approver_group=?,links=?,tags=?,updated_at=? WHERE id=?`, name, desc, owner, group, ls, ts, store.Ts(s.now()), x.ID)
	if in.ApproverGroup != nil && !x.Builtin && ptrStr(x.Group) != *in.ApproverGroup {
		s.audit(r, p, "product.approver_group_change", "product", x.ID, "success", map[string]any{"product": x.Slug, "from": x.Group, "to": *in.ApproverGroup})
	}
	s.audit(r, p, "product.update", "product", x.ID, "success", map[string]any{"product": x.Slug})
	n, _ := s.productBySlug(x.Slug)
	s.publishProduct(n)
	writeJSON(w, 200, s.productJSON(p, n))
}

func (s *server) deleteProduct(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := authz.Require(s.db, p, "", "global_admin"); err != nil {
		s.deny(w, r, p, "product.delete", err)
		return
	}
	x, err := s.productBySlug(r.PathValue("product"))
	if err != nil {
		s.audit(r, p, "product.delete", "product", r.PathValue("product"), "failed", nil)
		problem(w, 404, "not_found", "not found")
		return
	}
	var apps, grants int // grants are never deleted (GRT-026), so a product that ever had one stays
	s.db.QueryRow(`SELECT (SELECT count(*) FROM asset WHERE product_id=?1),(SELECT count(*) FROM access_grant WHERE product_id=?1)`, x.ID).Scan(&apps, &grants)
	if x.Builtin || apps > 0 || grants > 0 { // PRD-006, DOM-015
		s.audit(r, p, "product.delete", "product", x.ID, "failed", map[string]any{"product": x.Slug})
		problem(w, 409, "product_not_empty", "the product is built-in, has applications, or has grant history")
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		problem(w, 500, "internal", "begin failed")
		return
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM environment WHERE product_id=?`, x.ID)
	tx.Exec(`DELETE FROM role_binding WHERE product_id=?`, x.ID)
	tx.Exec(`DELETE FROM product WHERE id=?`, x.ID)
	if tx.Commit() != nil {
		problem(w, 500, "internal", "commit failed")
		return
	}
	s.audit(r, p, "product.delete", "product", x.ID, "success", map[string]any{"product": x.Slug})
	s.publish(events.Event{Type: "product.removed", At: s.now(), Product: x.ID, Data: map[string]string{"slug": x.Slug}})
	w.WriteHeader(204)
}

// --- applications ---

// canSeeApp: a global/product role, or a namespace role on the home namespace or any stack namespace.
func (s *server) canSeeApp(p *auth.Principal, a assetRow) bool {
	if s.prodRole(p, a.ProdID) > 0 || a.Namespace != nil && authz.Effective(s.db, p, *a.Namespace) > 0 {
		return true
	}
	rows, err := s.db.Query(`SELECT DISTINCT n.slug FROM stack st JOIN namespace n ON n.id=st.namespace_id WHERE st.asset_id=? AND st.state!='removed'`, a.ID)
	if err != nil {
		return false
	}
	var ns []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		ns = append(ns, n)
	}
	rows.Close()
	return slices.ContainsFunc(ns, func(n string) bool { return authz.Effective(s.db, p, n) > 0 })
}

// visibleApps lists a product's applications the caller can see, slug > after, at most limit+1 when limit > 0.
func (s *server) visibleApps(p *auth.Principal, prodID, after string, limit int) []assetRow {
	rows, err := s.db.Query(`SELECT `+assetCols+assetFrom+`WHERE a.product_id=? AND a.slug>? ORDER BY a.slug`, prodID, after)
	if err != nil {
		return nil
	}
	var all []assetRow
	for rows.Next() {
		a, _ := scanAsset(rows)
		all = append(all, a)
	}
	rows.Close()
	var out []assetRow
	for _, a := range all {
		if s.canSeeApp(p, a) {
			out = append(out, a)
			if limit > 0 && len(out) == limit+1 {
				break
			}
		}
	}
	return out
}

func (s *server) listApplications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "viewer", "application.list")
	if !ok {
		return
	}
	limit, after, ok := page(w, r)
	if !ok {
		return
	}
	apps := s.visibleApps(p, x.ID, after, limit)
	next := (*string)(nil)
	if len(apps) > limit {
		apps = apps[:limit]
		next = cursor(apps[limit-1].Slug)
	}
	if apps == nil {
		apps = []assetRow{}
	}
	writeJSON(w, 200, map[string]any{"items": apps, "next_cursor": next})
}

func (s *server) createApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "operator", "asset.create")
	if !ok {
		return
	}
	var in assetIn
	if !decode(w, r, &in) {
		return
	}
	if in.Product != "" && in.Product != x.Slug {
		problem(w, 400, "invalid_body", "product in the body must match the path")
		return
	}
	s.insertApp(w, r, p, x, nil, in)
}

// loadApp resolves {product}/{app}: visible (404), then appRole >= min (403).
func (s *server) loadApp(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (assetRow, bool) {
	x, ok := s.loadProduct(w, r, p, "viewer", action)
	if !ok {
		return assetRow{}, false
	}
	a, err := scanAsset(s.db.QueryRow(`SELECT `+assetCols+assetFrom+`WHERE a.product_id=? AND a.slug=?`, x.ID, r.PathValue("app")))
	if err != nil || !s.canSeeApp(p, a) {
		s.audit(r, p, action, "asset", r.PathValue("app"), "denied", map[string]any{"product": x.Slug})
		problem(w, 404, "not_found", "not found")
		return a, false
	}
	if min != "viewer" && s.appRole(p, a) < authz.Rank(min) {
		s.auditNS(r, p, ptrStr(a.Namespace), action, "asset", a.ID, "denied", map[string]any{"product": x.Slug})
		problem(w, 403, "forbidden", "insufficient role")
		return a, false
	}
	return a, true
}

func (s *server) getApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if a, ok := s.loadApp(w, r, p, "viewer", "asset.get"); ok {
		writeJSON(w, 200, a)
	}
}

func (s *server) updateApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if a, ok := s.loadApp(w, r, p, "operator", "asset.update"); ok {
		s.patchApp(w, r, p, a)
	}
}

func (s *server) deleteApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if a, ok := s.loadApp(w, r, p, "admin", "asset.delete"); ok {
		s.removeApp(w, r, p, a)
	}
}

// --- environments ---

type envRec struct {
	ID, ProdID, Product, Name, Tier, TargetID, Target, NSID, NS, EffTier string
	Position                                                             int
}

const envSel = `SELECT e.id,e.product_id,p.slug,e.name,e.tier,e.target_id,COALESCE(t.name,''),e.namespace_id,n.slug,e.position,
	CASE WHEN e.tier='prod' OR n.environment_tier='prod' OR t.environment_tier='prod' THEN 'prod' ELSE 'non-prod' END
	FROM environment e JOIN product p ON p.id=e.product_id JOIN namespace n ON n.id=e.namespace_id LEFT JOIN target t ON t.id=e.target_id `

func scanEnv(row interface{ Scan(...any) error }) (e envRec, err error) {
	err = row.Scan(&e.ID, &e.ProdID, &e.Product, &e.Name, &e.Tier, &e.TargetID, &e.Target, &e.NSID, &e.NS, &e.Position, &e.EffTier)
	return
}

func (e envRec) JSON() map[string]any {
	return map[string]any{"id": e.ID, "product": e.Product, "name": e.Name, "tier": e.Tier, "target": e.Target, "namespace": e.NS,
		"order": e.Position, "effective_tier": e.EffTier}
}

func (s *server) publishEnv(e envRec, deleted bool) {
	d := e.JSON()
	if deleted {
		d["deleted"] = true
	}
	s.publish(events.Event{Type: "environment.updated", At: s.now(), Product: e.ProdID, Data: d})
}

func (s *server) listEnvironments(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "viewer", "environment.list")
	if !ok {
		return
	}
	rows, err := s.db.Query(envSel+`WHERE e.product_id=? ORDER BY e.position,e.name`, x.ID)
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	var all []envRec
	for rows.Next() {
		e, _ := scanEnv(rows)
		all = append(all, e)
	}
	rows.Close()
	items := []map[string]any{}
	roleOnProduct := s.prodRole(p, x.ID) > 0
	for _, e := range all {
		if roleOnProduct || authz.Effective(s.db, p, e.NS) > 0 {
			items = append(items, e.JSON())
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *server) loadEnv(w http.ResponseWriter, r *http.Request, p *auth.Principal, min, action string) (prodRec, envRec, bool) {
	x, ok := s.loadProduct(w, r, p, "viewer", action)
	if !ok {
		return x, envRec{}, false
	}
	e, err := scanEnv(s.db.QueryRow(envSel+`WHERE e.product_id=? AND e.name=?`, x.ID, r.PathValue("env")))
	if err != nil || s.prodRole(p, x.ID) == 0 && authz.Effective(s.db, p, e.NS) == 0 {
		problem(w, 404, "not_found", "not found")
		return x, e, false
	}
	if min != "viewer" && s.prodRole(p, x.ID) < authz.Rank(min) {
		s.audit(r, p, action, "environment", e.ID, "denied", map[string]any{"product": x.Slug})
		problem(w, 403, "forbidden", "insufficient role")
		return x, e, false
	}
	return x, e, true
}

func (s *server) getEnvironment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if _, e, ok := s.loadEnv(w, r, p, "viewer", "environment.get"); ok {
		writeJSON(w, 200, e.JSON())
	}
}

// placement resolves target name and namespace slug to ids (400 on unknown).
func (s *server) placement(w http.ResponseWriter, target, ns string) (tid, nsID string, ok bool) {
	if s.db.QueryRow(`SELECT id FROM target WHERE name=?`, target).Scan(&tid) != nil || tid == "" {
		problem(w, 400, "unknown_target", "no such target")
		return
	}
	if s.db.QueryRow(`SELECT id FROM namespace WHERE slug=?`, ns).Scan(&nsID) != nil {
		problem(w, 400, "unknown_namespace", "no such namespace")
		return
	}
	return tid, nsID, true
}

func (s *server) createEnvironment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "admin", "environment.create")
	if !ok {
		return
	}
	var in struct {
		Name, Tier, Target, Namespace string
		ConfirmProd                   string `json:"confirm_prod"`
		Order                         *int   `json:"order"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch {
	case x.Builtin:
		problem(w, 400, "invalid_body", "the built-in unassigned product has no environments")
		return
	case !slugRe.MatchString(in.Name) || in.Tier != "prod" && in.Tier != "non-prod":
		problem(w, 400, "invalid_body", "name (slug) and tier (prod|non-prod) are required")
		return
	case in.Tier == "prod" && in.ConfirmProd != x.Slug: // PRD-046
		problem(w, 400, "confirm_prod_required", "confirm_prod must equal the product slug when tier is prod")
		return
	}
	tid, nsID, ok := s.placement(w, in.Target, in.Namespace)
	if !ok {
		return
	}
	pos := 0
	if in.Order != nil {
		pos = *in.Order
	} else {
		s.db.QueryRow(`SELECT COALESCE(MAX(position),0)+1 FROM environment WHERE product_id=?`, x.ID).Scan(&pos)
	}
	id, now := auth.NewID("env"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO environment(id,product_id,name,tier,target_id,namespace_id,position,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, x.ID, in.Name, in.Tier, tid, nsID, pos, now, now); err != nil {
		s.audit(r, p, "environment.create", "environment", in.Name, "failed", map[string]any{"product": x.Slug})
		problem(w, 409, "environment_exists", "the product already has this environment name or placement")
		return
	}
	s.audit(r, p, "environment.create", "environment", id, "success", map[string]any{"product": x.Slug, "name": in.Name, "tier": in.Tier, "target": in.Target, "namespace": in.Namespace})
	e, _ := scanEnv(s.db.QueryRow(envSel+`WHERE e.id=?`, id))
	s.publishEnv(e, false)
	writeJSON(w, 201, e.JSON())
}

func (s *server) updateEnvironment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, e, ok := s.loadEnv(w, r, p, "admin", "environment.update")
	if !ok {
		return
	}
	var in struct {
		Tier, Target, Namespace *string
		ConfirmProd             *string `json:"confirm_prod"`
		Order                   *int    `json:"order"`
	}
	if !decode(w, r, &in) {
		return
	}
	tier, target, ns, pos := e.Tier, e.Target, e.NS, e.Position
	if in.Tier != nil {
		tier = *in.Tier
	}
	if in.Target != nil {
		target = *in.Target
	}
	if in.Namespace != nil {
		ns = *in.Namespace
	}
	if in.Order != nil {
		pos = *in.Order
	}
	switch {
	case tier != "prod" && tier != "non-prod":
		problem(w, 400, "invalid_body", "tier must be prod or non-prod")
		return
	case tier == "prod" && e.Tier != "prod" && (in.ConfirmProd == nil || *in.ConfirmProd != x.Slug): // PRD-046, D-122
		problem(w, 400, "confirm_prod_required", "confirm_prod must equal the product slug when raising the tier to prod")
		return
	case tier != "prod" && e.Tier == "prod" && authz.Effective(s.db, p, "") != 4: // lowering needs global_admin
		s.audit(r, p, "environment.update", "environment", e.ID, "denied", map[string]any{"product": x.Slug, "reason": "tier_lowered"})
		problem(w, 403, "forbidden", "only global_admin lowers an environment tier")
		return
	}
	tid, nsID, ok := s.placement(w, target, ns)
	if !ok {
		return
	}
	var live int
	s.db.QueryRow(`SELECT count(*) FROM stack WHERE environment_id=? AND state!='removed'`, e.ID).Scan(&live)
	if live > 0 && (tid != e.TargetID || nsID != e.NSID) { // PRD-046
		problem(w, 409, "environment_in_use", "the environment has stacks; create a new environment and deploy there")
		return
	}
	if _, err := s.db.Exec(`UPDATE environment SET tier=?,target_id=?,namespace_id=?,position=?,updated_at=? WHERE id=?`, tier, tid, nsID, pos, store.Ts(s.now()), e.ID); err != nil {
		problem(w, 409, "environment_exists", "the product already has an environment at this placement")
		return
	}
	s.audit(r, p, "environment.update", "environment", e.ID, "success", map[string]any{"product": x.Slug, "name": e.Name, "from_tier": e.Tier, "to_tier": tier, "target": target, "namespace": ns})
	n, _ := scanEnv(s.db.QueryRow(envSel+`WHERE e.id=?`, e.ID))
	s.publishEnv(n, false)
	s.publishStacksOfEnv(e.ID) // the effective tier of its stacks may have changed
	writeJSON(w, 200, n.JSON())
}

func (s *server) publishStacksOfEnv(envID string) {
	rows, err := s.db.Query(stackSel+`WHERE st.environment_id=? AND st.state!='removed'`, envID)
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

func (s *server) deleteEnvironment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, e, ok := s.loadEnv(w, r, p, "admin", "environment.delete")
	if !ok {
		return
	}
	var live int
	s.db.QueryRow(`SELECT count(*) FROM stack WHERE environment_id=? AND state!='removed'`, e.ID).Scan(&live)
	if live > 0 {
		s.audit(r, p, "environment.delete", "environment", e.ID, "failed", map[string]any{"product": x.Slug})
		problem(w, 409, "environment_in_use", "the environment still has stacks")
		return
	}
	s.db.Exec(`UPDATE stack SET environment_id=NULL WHERE environment_id=?`, e.ID) // removed stacks keep their row
	s.db.Exec(`DELETE FROM environment WHERE id=?`, e.ID)
	s.audit(r, p, "environment.delete", "environment", e.ID, "success", map[string]any{"product": x.Slug, "name": e.Name})
	s.publishEnv(e, true)
	w.WriteHeader(204)
}

// --- placing an application (PRD-044, PRD-012) ---

// productAllowed is POL-R15: a 403 `product_not_allowed_in_namespace` is written when the namespace refuses the product.
func (s *server) productAllowed(w http.ResponseWriter, r *http.Request, p *auth.Principal, nsID, nsSlug, product, action string) bool {
	rules, err := s.rules(nsID)
	if err != nil {
		problem(w, 500, "internal", "policy unavailable")
		return false
	}
	if !rules.ProductAllowed(product) {
		s.auditNS(r, p, nsSlug, action, "stack", "", "denied", map[string]any{"reason": "product_not_allowed_in_namespace", "product": product})
		problem(w, 403, "product_not_allowed_in_namespace", "the namespace policy does not allow this product")
		return false
	}
	return true
}

// insertStack creates the draft stack and answers 201 (shared by the namespaced and the environment routes).
func (s *server) insertStack(w http.ResponseWriter, r *http.Request, p *auth.Principal, nsID, nsSlug string, a assetRow, tid, slug string, envID any) {
	var kind string
	if s.db.QueryRow(`SELECT kind FROM target WHERE id=?`, tid).Scan(&kind) != nil {
		problem(w, 400, "unknown_target", "no such target")
		return
	}
	if a.ManagedBy != "control8" { // DOM-005, PRD-005: register-only applications have no stacks
		code := "tool_managed"
		if a.ManagedBy == "argo" {
			code = "argo_managed"
		}
		s.auditNS(r, p, nsSlug, "stack.create", "stack", slug, "failed", map[string]any{"reason": code, "product": a.Product})
		problem(w, 409, code, "asset is managed by "+a.ManagedBy+"; it has no stacks")
		return
	}
	id, now := auth.NewID("stk"), store.Ts(s.now())
	if _, err := s.db.Exec(`INSERT INTO stack(id,namespace_id,asset_id,target_id,slug,runtime,k8s_namespace,environment_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, nsID, a.ID, tid, slug, kind, nsSlug, envID, now, now); err != nil {
		s.auditNS(r, p, nsSlug, "stack.create", "stack", slug, "failed", map[string]any{"product": a.Product})
		problem(w, 409, "stack_exists", "stack already exists")
		return
	}
	s.auditNS(r, p, nsSlug, "stack.create", "stack", id, "success", map[string]any{"slug": slug, "asset": a.Slug, "product": a.Product})
	x, _ := scanStack(s.db.QueryRow(stackSel+`WHERE st.id=?`, id))
	s.publishStack(x)
	writeJSON(w, 201, x.JSON())
}

// placeApplication: operator on the product and the namespace policy admits the product (PRD-012); target and
// namespace come from the environment.
func (s *server) placeApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	a, ok := s.loadApp(w, r, p, "operator", "stack.create")
	if !ok {
		return
	}
	var in struct{ Environment, Slug string }
	if !decode(w, r, &in) {
		return
	}
	if a.Product == "unassigned" || in.Environment == "" || in.Slug != "" && !slugRe.MatchString(in.Slug) {
		problem(w, 400, "invalid_body", "environment is required (the unassigned product has none); slug must be a slug")
		return
	}
	e, err := scanEnv(s.db.QueryRow(envSel+`WHERE e.product_id=? AND e.name=?`, a.ProdID, in.Environment))
	if err != nil {
		problem(w, 400, "unknown_environment", "no such environment in this product")
		return
	}
	if !s.productAllowed(w, r, p, e.NSID, e.NS, a.Product, "stack.create") {
		return
	}
	slug := in.Slug
	if slug == "" {
		slug = a.Slug
	}
	s.insertStack(w, r, p, e.NSID, e.NS, a, e.TargetID, slug, e.ID)
}

// --- overview matrix (PRD-030, PRD-031) ---

var digestRe = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

func (s *server) productOverview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	x, ok := s.loadProduct(w, r, p, "viewer", "product.overview")
	if !ok {
		return
	}
	columns := []map[string]any{}
	rows, _ := s.db.Query(envSel+`WHERE e.product_id=? ORDER BY e.position,e.name`, x.ID)
	var envs []envRec
	for rows != nil && rows.Next() {
		e, _ := scanEnv(rows)
		envs = append(envs, e)
	}
	if rows != nil {
		rows.Close()
	}
	roleOnProduct := s.prodRole(p, x.ID) > 0
	for _, e := range envs {
		if roleOnProduct || authz.Effective(s.db, p, e.NS) > 0 {
			columns = append(columns, map[string]any{"name": e.Name, "tier": e.Tier, "target": e.Target, "namespace": e.NS, "order": e.Position})
		}
	}
	stacks := s.visibleStacks(p, x.ID)
	cellsOf := map[string][]map[string]any{}
	legacy := map[string]bool{}
	t := map[string]int{"apps": 0, "stacks": len(stacks), "prod": 0, "non_prod": 0, "unhealthy": 0, "open_findings": 0, "active_grants": 0}
	for _, st := range stacks {
		var drift []any
		json.Unmarshal([]byte(st.Drift), &drift)
		var svcs []struct {
			Image      *string `json:"image_ref"`
			ImageID    *string `json:"image_id"`
			Containers []struct{ Name, Image string }
		}
		json.Unmarshal([]byte(st.Services), &svcs)
		images := []map[string]any{}
		for _, sv := range svcs {
			var digest any
			if d := digestRe.FindString(ptrStr(sv.ImageID)); d != "" {
				digest = d
			}
			for _, c := range sv.Containers {
				images = append(images, map[string]any{"container": c.Name, "image": c.Image, "digest": digest})
			}
		}
		var ro any
		var raw *string
		s.db.QueryRow(`SELECT rollout FROM deployment WHERE stack_id=? AND rollout IS NOT NULL ORDER BY rowid DESC LIMIT 1`, st.ID).Scan(&raw)
		if raw != nil {
			var rs struct {
				State  string  `json:"state"`
				Result *string `json:"result"`
			}
			if json.Unmarshal([]byte(*raw), &rs) == nil {
				ro = map[string]any{"state": rs.State, "result": rs.Result}
			}
		}
		var pending int
		s.db.QueryRow(`SELECT count(*) FROM access_grant WHERE product_id=? AND target_id=? AND namespace_id=? AND status='requested'`, st.ProdID, st.TargetID, st.NSID).Scan(&pending)
		health := st.Health
		if st.Frozen && health != "unknown" {
			health = "frozen"
		}
		cell := map[string]any{"environment": st.Env, "target": st.Target, "namespace": st.NS, "stack": st.Slug, "state": st.State,
			"safety_profile": st.Profile, "desired_revision": st.Desired, "images": images, "health": health, "drift_count": len(drift),
			"rollout": ro, "pending_grants": pending, "open_findings": 0}
		cellsOf[st.Asset] = append(cellsOf[st.Asset], cell)
		if st.Env == nil && !legacy[st.Target+"/"+st.NS] { // one "legacy" column per placement without environment
			legacy[st.Target+"/"+st.NS] = true
			columns = append(columns, map[string]any{"name": nil, "tier": st.Tier, "target": st.Target, "namespace": st.NS, "order": 1000 + len(legacy)})
		}
		if st.Tier == "prod" {
			t["prod"]++
		} else {
			t["non_prod"]++
		}
		if st.Health == "degraded" || st.Health == "drifted" {
			t["unhealthy"]++
		}
	}
	apps := []map[string]any{}
	for _, a := range s.visibleApps(p, x.ID, "", 0) {
		cells := cellsOf[a.Slug]
		if cells == nil {
			cells = []map[string]any{}
		}
		apps = append(apps, map[string]any{"slug": a.Slug, "name": a.Name, "managed_by": a.ManagedBy, "cells": cells})
	}
	t["apps"] = len(apps)
	now, ag := store.Ts(s.now()), 0
	s.db.QueryRow(`SELECT count(*) FROM access_grant WHERE product_id=? AND status IN ('approved','active') AND starts_at<=? AND expires_at>?`, x.ID, now, now).Scan(&ag)
	t["active_grants"] = ag
	writeJSON(w, 200, map[string]any{"product": s.productJSON(p, x), "environments": columns, "applications": apps, "totals": t})
}
