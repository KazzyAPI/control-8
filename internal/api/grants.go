package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/authz"
	"github.com/KazzyAPI/control-8/internal/events"
	"github.com/KazzyAPI/control-8/internal/store"
)

const (
	grantMinMinutes = 15
	grantMaxAhead   = 7 * 24 * time.Hour
	grantUndecided  = 24 * time.Hour // GRT states: requested and not decided in 24 h expires
)

func (s *server) grantRoutes(reg func(string, http.HandlerFunc)) {
	reg("GET /api/v1/access-grants", s.api(s.listGrants))
	reg("POST /api/v1/access-grants", s.api(s.requestGrant))
	reg("GET /api/v1/access-grants/mine", s.api(s.myGrants))
	reg("GET /api/v1/access-grants/{id}", s.api(s.getGrant))
	reg("POST /api/v1/access-grants/{id}/approve", s.api(s.approveGrant))
	reg("POST /api/v1/access-grants/{id}/deny", s.api(s.denyGrant))
	reg("POST /api/v1/access-grants/{id}/revoke", s.api(s.revokeGrant))
}

func nz(s string) any { return emptyNil(s) }

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// grantRef is what a gated action ran under: a grant, or the break-glass bypass (GRT-014, GRT-016).
type grantRef struct{ ID, Bypass string }

func (g grantRef) details(d map[string]any) map[string]any {
	if d == nil {
		d = map[string]any{}
	}
	if g.ID != "" {
		d["grant_id"] = g.ID
	}
	if g.Bypass != "" {
		d["grant_bypass"] = g.Bypass
	}
	return d
}

// grantUser is the user whose grants the principal acts with: tokens use their owner's (GRT-015, D-45).
func (s *server) grantUser(p *auth.Principal) string {
	if !p.IsToken() {
		return p.ID
	}
	var sub string
	s.db.QueryRow(`SELECT owner_sub FROM token WHERE id=?`, p.ID).Scan(&sub)
	id, _ := auth.OwnerOf(s.db, sub)
	return id
}

// grantOK is the prod gate (GRT-010, GRT-011, GRT-016): non-prod passes; break-glass bypasses; otherwise
// the caller needs an approved/active grant for exactly this (target, namespace) right now.
func (s *server) grantOK(w http.ResponseWriter, r *http.Request, p *auth.Principal, x stackRec, action string, gr *grantRef) bool {
	switch {
	case x.Tier != "prod":
		return true
	case p.BreakGlass:
		gr.Bypass = "break_glass"
		return true
	}
	now := store.Ts(s.now())
	var id string
	if uid := s.grantUser(p); uid != "" {
		s.db.QueryRow(`SELECT id FROM access_grant WHERE requester_id=? AND target_id=? AND namespace_id=? AND status IN ('approved','active')
			AND starts_at<=? AND expires_at>? ORDER BY expires_at DESC LIMIT 1`, uid, x.TargetID, x.NSID, now, now).Scan(&id)
	}
	if id != "" {
		gr.ID = id
		return true
	}
	s.auditNS(r, p, x.NS, action, "stack", x.ID, "denied", map[string]any{"reason": "grant_required"})
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(403)
	json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "Forbidden", "status": 403, "code": "grant_required",
		"detail": "prod deployments need an active access grant", "scope": map[string]string{"target": x.Target, "namespace": x.NS}})
	return false
}

// grantStillValid is the worker re-check (GRT-012): the grant recorded at request time must still cover now.
func (s *server) grantStillValid(d depRec) bool {
	if d.Tier != "prod" || d.GrantBypass != nil {
		return true
	}
	if d.GrantID == nil {
		return false
	}
	now := store.Ts(s.now())
	var n int
	s.db.QueryRow(`SELECT count(*) FROM access_grant WHERE id=? AND status IN ('approved','active') AND starts_at<=? AND expires_at>?`, *d.GrantID, now, now).Scan(&n)
	return n == 1
}

type grantRec struct {
	ID, Requester, Target, NS, NSID, TargetID, Reason, Status, ApproverGroup, Created string
	Tier                                                                              string
	Ticket, StartsAt, ExpiresAt, Approver, DecidedAt, Note, RevBy, RevAt, RevReason   *string
	Minutes                                                                           int
	Rowid                                                                             int64
}

const grantSel = `SELECT g.id,g.requester_id,t.name,n.slug,g.namespace_id,g.target_id,g.reason,g.status,g.approver_group,g.created_at,
	CASE WHEN n.environment_tier='prod' OR t.environment_tier='prod' THEN 'prod' ELSE 'non-prod' END,
	g.ticket_ref,g.starts_at,g.expires_at,g.approver_id,g.decided_at,g.decision_note,g.revoked_by,g.revoked_at,g.revoke_reason,g.duration_minutes,g.rowid
	FROM access_grant g JOIN namespace n ON n.id=g.namespace_id JOIN target t ON t.id=g.target_id `

func scanGrant(row interface{ Scan(...any) error }) (g grantRec, err error) {
	err = row.Scan(&g.ID, &g.Requester, &g.Target, &g.NS, &g.NSID, &g.TargetID, &g.Reason, &g.Status, &g.ApproverGroup, &g.Created, &g.Tier,
		&g.Ticket, &g.StartsAt, &g.ExpiresAt, &g.Approver, &g.DecidedAt, &g.Note, &g.RevBy, &g.RevAt, &g.RevReason, &g.Minutes, &g.Rowid)
	return
}

// eff is the status computed from the clock (GRT-023); the sweeper persists it.
func (g grantRec) eff(now time.Time) string {
	n := store.Ts(now)
	switch g.Status {
	case "requested":
		if n >= store.Ts(mustTs(g.Created).Add(grantUndecided)) {
			return "expired"
		}
	case "approved", "active":
		switch {
		case n >= *g.ExpiresAt:
			return "expired"
		case n >= *g.StartsAt:
			return "active"
		}
		return "approved"
	}
	return g.Status
}

func mustTs(s string) time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05.000Z", s)
	return t
}

func (g grantRec) JSON(now time.Time) map[string]any {
	return map[string]any{"id": g.ID, "requester": g.Requester, "target": g.Target, "namespace": g.NS, "environment_tier": g.Tier,
		"reason": g.Reason, "ticket_ref": g.Ticket, "duration_minutes": g.Minutes, "status": g.eff(now), "starts_at": g.StartsAt,
		"expires_at": g.ExpiresAt, "approver_group": g.ApproverGroup, "approver": g.Approver, "decided_at": g.DecidedAt,
		"decision_note": g.Note, "revoked_by": g.RevBy, "revoked_at": g.RevAt, "revoke_reason": g.RevReason, "created_at": g.Created}
}

// canSee: the requester, a member of the namespace's current approver_group, or global_admin (09 API table).
func (s *server) canSeeGrant(p *auth.Principal, g grantRec) bool {
	return s.grantUser(p) == g.Requester || authz.Effective(s.db, p, "") == 4 || slices.Contains(s.approverFor(p.Groups), g.NS)
}

// publishGrant emits access_grant.updated (Requester + approver group + global_admin see it, EVT filter) and,
// for webhook events, the signed notification.
func (s *server) publishGrant(id, hook string) {
	g, err := scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, id))
	if err != nil {
		return
	}
	j := g.JSON(s.now())
	s.publish(events.Event{Type: "access_grant.updated", At: s.now(), Namespace: g.NS, Data: j, Requester: g.Requester})
	if hook != "" {
		s.notify("access_grant."+hook, j)
	}
}

// notify POSTs the signed webhook in the background (GRT-040, GRT-041); it never blocks or fails the caller.
func (s *server) notify(event string, grant map[string]any) {
	if s.cfg.WebhookURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"event": event, "at": store.Ts(s.now()), "grant": grant})
	mac := hmac.New(sha256.New, []byte(s.cfg.WebhookSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	backoff := s.hookBackoff
	if backoff == nil {
		backoff = []time.Duration{time.Second, 5 * time.Second}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		c := &http.Client{Timeout: 5 * time.Second}
		for i := 0; ; i++ {
			req, _ := http.NewRequest("POST", s.cfg.WebhookURL, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-C8-Signature", sig)
			if resp, err := c.Do(req); err == nil {
				resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					return
				}
			}
			if i >= len(backoff) {
				ns, _ := grant["namespace"].(string)
				s.auditSystem(ns, "webhook.failed", "access_grant", grant["id"].(string), "failed", map[string]any{"event": event})
				return
			}
			time.Sleep(backoff[i])
		}
	}()
}

// sweep persists time-driven transitions with system audit events (GRT-023). Safe to call at any time.
func (s *server) sweep() {
	rows, err := s.db.Query(grantSel + `WHERE g.status IN ('requested','approved','active')`)
	if err != nil {
		return
	}
	var open []grantRec
	for rows.Next() {
		g, _ := scanGrant(rows)
		open = append(open, g)
	}
	rows.Close()
	now := s.now()
	for _, g := range open {
		to := g.eff(now)
		if to == g.Status {
			continue
		}
		res, _ := s.db.Exec(`UPDATE access_grant SET status=?,updated_at=? WHERE id=? AND status=?`, to, store.Ts(now), g.ID, g.Status)
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		action, hook := "access_grant.activate", ""
		if to == "expired" {
			action, hook = "access_grant.expire", "expired"
		}
		s.auditSystem(g.NS, action, "access_grant", g.ID, "success", map[string]any{"from": g.Status})
		s.publishGrant(g.ID, hook)
	}
}

// sweepLoop runs the 1-minute sweeper (GRT-023); shortcut: never stopped, it ends with the process.
func (s *server) sweepLoop() {
	tick, _ := s.tick(time.Minute)
	for {
		s.sweep()
		<-tick
	}
}

func (s *server) requestGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Target, Namespace, Reason, TicketRef string
		Minutes                              int       `json:"duration_minutes"`
		StartsAt                             time.Time `json:"starts_at"`
	}
	if !decode(w, r, &in) {
		return
	}
	var nsID, group, ntier string
	var nsGroup *string
	if s.db.QueryRow(`SELECT id,approver_group,environment_tier FROM namespace WHERE slug=?`, in.Namespace).Scan(&nsID, &nsGroup, &ntier) != nil {
		problem(w, 404, "not_found", "no such namespace")
		return
	}
	if err := authz.Require(s.db, p, in.Namespace, "operator"); err != nil { // GRT-022
		s.denyNS(w, r, p, in.Namespace, "access_grant.request", err)
		return
	}
	var tgtID, ttier string
	if s.db.QueryRow(`SELECT id,environment_tier FROM target WHERE name=?`, in.Target).Scan(&tgtID, &ttier) != nil {
		problem(w, 400, "invalid_body", "no such target")
		return
	}
	now := s.now()
	max := int(s.cfg.GrantMax / time.Minute)
	switch {
	case utf8Len(in.Reason) < 10 || utf8Len(in.Reason) > 500, utf8Len(in.TicketRef) > 100:
		problem(w, 400, "invalid_body", "reason must be 10..500 characters, ticket_ref at most 100")
		return
	case in.Minutes < grantMinMinutes || in.Minutes > max:
		problem(w, 400, "invalid_body", "duration_minutes must be "+strconv.Itoa(grantMinMinutes)+".."+strconv.Itoa(max))
		return
	case !in.StartsAt.IsZero() && in.StartsAt.After(now.Add(grantMaxAhead)):
		problem(w, 400, "invalid_body", "starts_at must be at most 7 days ahead")
		return
	case ntier != "prod" && ttier != "prod":
		problem(w, 400, "invalid_body", "scope is not prod; no grant needed")
		return
	case nsGroup == nil || *nsGroup == "":
		s.auditNS(r, p, in.Namespace, "access_grant.request", "access_grant", "", "denied", map[string]any{"reason": "approver_group_missing"})
		problem(w, 409, "approver_group_missing", "the namespace has no approver_group")
		return
	}
	group = *nsGroup
	uid := s.grantUser(p)
	if uid == "" {
		problem(w, 403, "forbidden", "token owner is not resolved")
		return
	}
	s.sweep() // an expired-but-unswept grant must not block a new request (GRT-021)
	id := auth.NewID("agr")
	var rs any
	if !in.StartsAt.IsZero() {
		rs = store.Ts(in.StartsAt)
	}
	if _, err := s.db.Exec(`INSERT INTO access_grant(id,requester_id,target_id,namespace_id,reason,ticket_ref,duration_minutes,req_starts_at,status,approver_group,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,'requested',?,?,?)`, id, uid, tgtID, nsID, in.Reason, nz(in.TicketRef), in.Minutes, rs, group, store.Ts(now), store.Ts(now)); err != nil {
		s.auditNS(r, p, in.Namespace, "access_grant.request", "access_grant", "", "failed", map[string]any{"reason": "grant_exists"})
		problem(w, 409, "grant_exists", "an open grant already exists for this scope")
		return
	}
	s.auditNS(r, p, in.Namespace, "access_grant.request", "access_grant", id, "success", map[string]any{"target": in.Target, "duration_minutes": in.Minutes})
	s.publishGrant(id, "requested")
	g, _ := scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, id))
	w.Header().Set("Location", "/api/v1/access-grants/"+id)
	writeJSON(w, 201, g.JSON(now))
}

func utf8Len(s string) int { return len([]rune(s)) }

// loadGrant 404s when the caller may not see the grant (API-005).
func (s *server) loadGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) (grantRec, bool) {
	g, err := scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, r.PathValue("id")))
	if err != nil || !s.canSeeGrant(p, g) {
		problem(w, 404, "not_found", "not found")
		return g, false
	}
	return g, true
}

func (s *server) getGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if g, ok := s.loadGrant(w, r, p); ok {
		writeJSON(w, 200, g.JSON(s.now()))
	}
}

func (s *server) myGrants(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.grantList(w, r, p, true)
}

func (s *server) listGrants(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.grantList(w, r, p, false)
}

// grantList: global_admin sees all, approver-group members their namespaces, everyone their own; mine = own only.
func (s *server) grantList(w http.ResponseWriter, r *http.Request, p *auth.Principal, mine bool) {
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
	q := r.URL.Query()
	rows, err := s.db.Query(grantSel+`WHERE g.rowid<? AND (?='' OR n.slug=?) AND (?='' OR t.name=?) AND (?='' OR g.requester_id=?) ORDER BY g.rowid DESC`,
		before, q.Get("namespace"), q.Get("namespace"), q.Get("target"), q.Get("target"), q.Get("requester"), q.Get("requester"))
	if err != nil {
		problem(w, 500, "internal", "query failed")
		return
	}
	var all []grantRec
	for rows.Next() {
		g, _ := scanGrant(rows)
		all = append(all, g)
	}
	rows.Close()
	uid, now := s.grantUser(p), s.now()
	admin, appr := authz.Effective(s.db, p, "") == 4, s.approverFor(p.Groups)
	items, next := []map[string]any{}, (*string)(nil)
	for _, g := range all {
		if !(g.Requester == uid || !mine && (admin || slices.Contains(appr, g.NS))) || q.Get("status") != "" && g.eff(now) != q.Get("status") {
			continue
		}
		if len(items) == limit {
			next = cursor(strconv.FormatInt(items[limit-1]["_rowid"].(int64), 10))
			break
		}
		m := g.JSON(now)
		m["_rowid"] = g.Rowid
		items = append(items, m)
	}
	for _, m := range items {
		delete(m, "_rowid")
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

// decide is the shared front of approve and deny: GRT-020 (non-approvers get 403, not 404) (session user, current approver group, not the requester).
func (s *server) decide(w http.ResponseWriter, r *http.Request, p *auth.Principal, action string) (grantRec, bool) {
	g, err := scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, r.PathValue("id")))
	if err != nil {
		problem(w, 404, "not_found", "not found")
		return g, false
	}
	deny := func(reason, msg string) (grantRec, bool) {
		s.auditNS(r, p, g.NS, action, "access_grant", g.ID, "denied", map[string]any{"reason": reason})
		problem(w, 403, reason, msg)
		return g, false
	}
	switch {
	case p.IsToken():
		return deny("token_not_allowed", "tokens cannot decide grants")
	case p.BreakGlass:
		return deny("break_glass_not_allowed", "break-glass sessions cannot decide grants")
	case !slices.Contains(s.approverFor(p.Groups), g.NS):
		return deny("forbidden", "only members of the namespace approver group can decide grants")
	case p.ID == g.Requester:
		return deny("self_approval", "the approver must differ from the requester")
	case g.eff(s.now()) != "requested":
		problem(w, 409, "invalid_state", "grant is not awaiting a decision")
		return g, false
	}
	return g, true
}

func (s *server) approveGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	g, ok := s.decide(w, r, p, "access_grant.approve")
	if !ok {
		return
	}
	var in struct {
		Minutes *int   `json:"duration_minutes"`
		Note    string `json:"note"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	mins := g.Minutes
	if in.Minutes != nil {
		if *in.Minutes < grantMinMinutes || *in.Minutes > g.Minutes { // GRT-025: shorten only
			problem(w, 400, "invalid_body", "duration_minutes may only shorten the request (min 15)")
			return
		}
		mins = *in.Minutes
	}
	if utf8Len(in.Note) > 500 {
		problem(w, 400, "invalid_body", "note is at most 500 characters")
		return
	}
	now := s.now()
	var req string
	s.db.QueryRow(`SELECT COALESCE(req_starts_at,'') FROM access_grant WHERE id=?`, g.ID).Scan(&req)
	start := now
	if rs := mustTs(req); req != "" && rs.After(now) {
		start = rs
	}
	status := "active"
	if start.After(now) {
		status = "approved"
	}
	res, _ := s.db.Exec(`UPDATE access_grant SET status=?,starts_at=?,expires_at=?,duration_minutes=?,approver_id=?,decided_at=?,decision_note=?,updated_at=? WHERE id=? AND status='requested'`,
		status, store.Ts(start), store.Ts(start.Add(time.Duration(mins)*time.Minute)), mins, p.ID, store.Ts(now), nz(in.Note), store.Ts(now), g.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		problem(w, 409, "invalid_state", "grant is not awaiting a decision")
		return
	}
	s.auditNS(r, p, g.NS, "access_grant.approve", "access_grant", g.ID, "success", map[string]any{"duration_minutes": mins})
	s.publishGrant(g.ID, "approved")
	g, _ = scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, g.ID))
	writeJSON(w, 200, g.JSON(now))
}

func (s *server) denyGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	g, ok := s.decide(w, r, p, "access_grant.deny")
	if !ok {
		return
	}
	var in struct{ Note string }
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Note) == "" || utf8Len(in.Note) > 500 {
		problem(w, 400, "invalid_body", "note is required (at most 500 characters)")
		return
	}
	now := store.Ts(s.now())
	res, _ := s.db.Exec(`UPDATE access_grant SET status='denied',approver_id=?,decided_at=?,decision_note=?,updated_at=? WHERE id=? AND status='requested'`, p.ID, now, in.Note, now, g.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		problem(w, 409, "invalid_state", "grant is not awaiting a decision")
		return
	}
	s.auditNS(r, p, g.NS, "access_grant.deny", "access_grant", g.ID, "success", nil)
	s.publishGrant(g.ID, "denied")
	g, _ = scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, g.ID))
	writeJSON(w, 200, g.JSON(s.now()))
}

// revokeGrant: the requester, an approver-group member or global_admin (GRT-024); also cancels a requested grant.
func (s *server) revokeGrant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	g, ok := s.loadGrant(w, r, p)
	if !ok {
		return
	}
	var in struct{ Reason string }
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Reason) == "" || utf8Len(in.Reason) > 500 {
		problem(w, 400, "invalid_body", "reason is required (at most 500 characters)")
		return
	}
	if !(s.grantUser(p) == g.Requester || authz.Effective(s.db, p, "") == 4 || slices.Contains(s.approverFor(p.Groups), g.NS)) {
		s.auditNS(r, p, g.NS, "access_grant.revoke", "access_grant", g.ID, "denied", nil)
		problem(w, 403, "forbidden", "not allowed to revoke this grant")
		return
	}
	if e := g.eff(s.now()); e != "requested" && e != "approved" && e != "active" {
		problem(w, 409, "invalid_state", "grant is "+e)
		return
	}
	now := store.Ts(s.now())
	res, _ := s.db.Exec(`UPDATE access_grant SET status='revoked',revoked_by=?,revoked_at=?,revoke_reason=?,updated_at=? WHERE id=? AND status IN ('requested','approved','active')`, p.ID, now, in.Reason, now, g.ID)
	if n, _ := res.RowsAffected(); n == 0 {
		problem(w, 409, "invalid_state", "grant is not open")
		return
	}
	s.auditNS(r, p, g.NS, "access_grant.revoke", "access_grant", g.ID, "success", map[string]any{"was": g.Status})
	s.publishGrant(g.ID, "revoked")
	g, _ = scanGrant(s.db.QueryRow(grantSel+`WHERE g.id=?`, g.ID))
	writeJSON(w, 200, g.JSON(s.now()))
}

// activeGrants feeds /me (Principal.active_grants).
func (s *server) activeGrants(p *auth.Principal) []map[string]any {
	out := []map[string]any{}
	rows, err := s.db.Query(grantSel+`WHERE g.requester_id=? AND g.status IN ('approved','active') ORDER BY g.rowid DESC`, s.grantUser(p))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if g, _ := scanGrant(rows); g.eff(s.now()) == "active" {
			out = append(out, g.JSON(s.now()))
		}
	}
	return out
}
