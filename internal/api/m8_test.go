package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
)

type m8 struct {
	*m7
	ap, bp map[string]string
	clk    *atomic.Int64
}

func (m *m8) advance(d time.Duration) {
	m.clk.Add(int64(d))
	m.s.db.Exec(`UPDATE session SET expires_at='9999-01-01T00:00:00.000Z'`) // sessions must outlive the fake clock
}

// newM8: namespace a on a prod target with approver group "apr"; ap approves for a, bp only for b.
func newM8(t *testing.T) *m8 {
	m := &m8{m7: newM7(t), clk: &atomic.Int64{}}
	m.clk.Store(time.Now().UnixNano())
	m.s.now = func() time.Time { return time.Unix(0, m.clk.Load()) }
	m.s.cfg.GrantMax = 4 * time.Hour
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	m.s.db.Exec(`UPDATE namespace SET approver_group='apr' WHERE slug='a'`)
	m.s.db.Exec(`UPDATE namespace SET approver_group='bpr' WHERE slug='b'`)
	m.ap, m.bp = m.user("ap", `["apr"]`), m.user("bp", `["bpr"]`)
	m.revision(cm)
	return m
}

func (m *m8) grantReq(h map[string]string, body string) (int, map[string]any) {
	w := m.do("POST", "/api/v1/access-grants", body, h)
	var g map[string]any
	json.Unmarshal(w.Body.Bytes(), &g)
	return w.Code, g
}

const reqBody = `{"target":"home","namespace":"a","reason":"fix the thing","duration_minutes":%d}`

func body(min string) string { return strings.Replace(reqBody, "%d", min, 1) }

func (m *m8) act(h map[string]string, id, verb, b string) (int, map[string]any) {
	w := m.do("POST", "/api/v1/access-grants/"+id+"/"+verb, b, h)
	var g map[string]any
	json.Unmarshal(w.Body.Bytes(), &g)
	return w.Code, g
}

func (m *m8) prodDeploy(h map[string]string, n string) (int, string) {
	w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/deployments",
		`{"revision":`+n+`,"confirm_safety_profile":"as_is","confirm_environment_tier":"prod","reason":"go"}`, h)
	return w.Code, w.Body.String()
}

// grant requests and approves one for h (approved by ap).
func (m *m8) grant(h map[string]string, mins string) string {
	c, g := m.grantReq(h, body(mins))
	if c != 201 {
		m.t.Fatalf("request %d %v", c, g)
	}
	id := g["id"].(string)
	if c, g := m.act(m.ap, id, "approve", ""); c != 200 || g["status"] != "active" {
		m.t.Fatalf("approve %d %v", c, g)
	}
	return id
}

func (m *m8) auditHas(action, sub string) bool {
	var n int
	m.s.db.QueryRow(`SELECT count(*) FROM audit_event WHERE action=? AND details LIKE ?`, action, "%"+sub+"%").Scan(&n)
	return n > 0
}

// GRT-010, 011, 014, 020, 021, 022, 025, 027, 024: request/approve rules and a deploy under a grant.
func TestGRT_RequestApproveDeploy(t *testing.T) {
	m := newM8(t)
	c, b := m.prodDeploy(m.op, "1")
	if c != 403 || !strings.Contains(b, `"grant_required"`) || !strings.Contains(b, `"scope"`) { // GRT-011
		t.Fatalf("%d %s", c, b)
	}
	for name, tc := range map[string]struct {
		h    map[string]string
		body string
		want int
	}{
		"viewer (GRT-022)":    {m.viewer, body("60"), 403},
		"short reason":        {m.op, strings.Replace(body("60"), "fix the thing", "short", 1), 400},
		"under min duration":  {m.op, body("5"), 400},
		"over C8_GRANT_MAX":   {m.op, body("241"), 400},
		"start too far ahead": {m.op, strings.Replace(body("60"), "}", `,"starts_at":"`+m.s.now().Add(8*24*time.Hour).UTC().Format(time.RFC3339)+`"}`, 1), 400},
		"unknown target":      {m.op, strings.Replace(body("60"), "home", "nope", 1), 400},
		"outsider":            {m.other, body("60"), 403},
	} {
		if c, _ := m.grantReq(tc.h, tc.body); c != tc.want && !(name == "outsider" && c == 404) {
			t.Errorf("%s = %d want %d", name, c, tc.want)
		}
	}
	m.s.db.Exec(`UPDATE namespace SET approver_group=NULL WHERE slug='a'`) // GRT-027
	if c, _ := m.grantReq(m.op, body("60")); c != 409 {
		t.Fatalf("no approver_group = %d", c)
	}
	m.s.db.Exec(`UPDATE namespace SET approver_group='apr' WHERE slug='a'`)

	c, g := m.grantReq(m.op, body("60"))
	if c != 201 || g["status"] != "requested" || g["approver_group"] != "apr" || g["environment_tier"] != "prod" {
		t.Fatalf("%d %v", c, g)
	}
	id := g["id"].(string)
	if c, _ := m.grantReq(m.op, body("30")); c != 409 { // GRT-021
		t.Fatalf("second open grant = %d", c)
	}
	// GRT-020: self-approval, wrong group, break-glass, viewer-of-nothing
	m.s.db.Exec(`UPDATE "user" SET groups='["oa","apr"]' WHERE id='usr_oa'`)
	if c, _ := m.act(m.op, id, "approve", ""); c != 403 {
		t.Fatalf("self approve = %d", c)
	}
	m.s.db.Exec(`UPDATE "user" SET groups='["oa"]' WHERE id='usr_oa'`)
	for who, h := range map[string]map[string]string{"other-ns approver": m.bp, "break-glass": m.admin, "requester": m.op, "ns admin": m.aa} {
		if c, _ := m.act(h, id, "approve", ""); c != 403 {
			t.Errorf("%s approve = %d want 403", who, c)
		}
	}
	if m.audits("access_grant.approve", "denied") < 5 {
		t.Fatal("denied approvals not audited")
	}
	if c, _ := m.act(m.ap, id, "approve", `{"duration_minutes":300}`); c != 400 { // GRT-025
		t.Fatalf("lengthen = %d", c)
	}
	c, g = m.act(m.ap, id, "approve", `{"duration_minutes":30,"note":"ok"}`)
	if c != 200 || g["status"] != "active" || g["duration_minutes"] != float64(30) || g["approver"] != "usr_ap" ||
		g["expires_at"] != m.s.now().Add(30*time.Minute).UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("%d %v", c, g)
	}
	if c, _ := m.act(m.ap, id, "approve", ""); c != 409 {
		t.Fatalf("re-approve = %d", c)
	}

	// deploy under the grant: GRT-014 grant_id on deployment and audit
	c, b = m.prodDeploy(m.op, "1")
	if c != 202 || !strings.Contains(b, id) {
		t.Fatalf("%d %s", c, b)
	}
	m.s.wg.Wait()
	if !m.auditHas("deployment.request", id) || !m.auditHas("deployment.run", id) {
		t.Fatal("grant_id missing from audit")
	}
	// the grant is scoped: the other namespace's stack is unaffected, other users still refused
	if c, _ := m.prodDeploy(m.aa, "1"); c != 403 {
		t.Fatalf("aa without grant = %d", c)
	}
	// GRT-024: revoke by outsider no, by requester yes (reason required); revoked grant stops new deploys
	if c, _ := m.act(m.other, id, "revoke", `{"reason":"x"}`); c != 404 {
		t.Fatalf("outsider revoke = %d", c)
	}
	if c, _ := m.act(m.op, id, "revoke", `{}`); c != 400 {
		t.Fatalf("revoke without reason = %d", c)
	}
	if c, g := m.act(m.op, id, "revoke", `{"reason":"done"}`); c != 200 || g["status"] != "revoked" {
		t.Fatalf("%d %v", c, g)
	}
	if c, _ := m.prodDeploy(m.op, "1"); c != 403 {
		t.Fatalf("deploy after revoke = %d", c)
	}
	if c, _ := m.act(m.op, id, "revoke", `{"reason":"again"}`); c != 409 {
		t.Fatalf("re-revoke = %d", c)
	}
	// deny needs a note; a denied grant frees the scope
	_, g = m.grantReq(m.op, body("60"))
	if c, _ := m.act(m.ap, g["id"].(string), "deny", `{}`); c != 400 {
		t.Fatalf("deny without note = %d", c)
	}
	if c, g2 := m.act(m.ap, g["id"].(string), "deny", `{"note":"no"}`); c != 200 || g2["status"] != "denied" {
		t.Fatalf("deny %d %v", c, g2)
	}
	if m.audits("access_grant.request", "success") != 2 || m.audits("access_grant.deny", "success") != 1 || m.audits("access_grant.revoke", "success") != 1 {
		t.Fatal("grant audit events missing")
	}
}

// GRT-023 (computed on read, sweeper persists + audits as system), GRT-021 after expiry, GRT-026.
func TestGRT023_StatusClockAndSweeper(t *testing.T) {
	m := newM8(t)
	start := m.s.now().Add(time.Hour).UTC().Format(time.RFC3339)
	_, g := m.grantReq(m.op, strings.Replace(body("30"), "}", `,"starts_at":"`+start+`"}`, 1))
	id := g["id"].(string)
	c, g := m.act(m.ap, id, "approve", "")
	if c != 200 || g["status"] != "approved" { // future start
		t.Fatalf("%d %v", c, g)
	}
	if c, _ := m.prodDeploy(m.op, "1"); c != 403 {
		t.Fatalf("deploy before start = %d", c)
	}
	m.advance(61 * time.Minute)
	get := func() string {
		var x map[string]any
		json.Unmarshal(m.do("GET", "/api/v1/access-grants/"+id, "", m.op).Body.Bytes(), &x)
		return x["status"].(string)
	}
	if get() != "active" {
		t.Fatal("not active by clock")
	}
	var persisted string
	m.s.db.QueryRow(`SELECT status FROM access_grant WHERE id=?`, id).Scan(&persisted)
	if persisted != "approved" {
		t.Fatalf("persisted early: %s", persisted)
	}
	m.s.sweep()
	m.s.sweep() // idempotent
	if m.audits("access_grant.activate", "success") != 1 {
		t.Fatal("activate not audited once")
	}
	var actor string
	m.s.db.QueryRow(`SELECT actor_type FROM audit_event WHERE action='access_grant.activate'`).Scan(&actor)
	if actor != "system" {
		t.Fatalf("actor %s", actor)
	}
	m.advance(31 * time.Minute)
	if get() != "expired" {
		t.Fatal("not expired by clock")
	}
	m.s.sweep()
	if m.audits("access_grant.expire", "success") != 1 {
		t.Fatal("expire not audited")
	}
	if c, _ := m.prodDeploy(m.op, "1"); c != 403 {
		t.Fatalf("deploy after expiry = %d", c)
	}
	if c, _ := m.act(m.op, id, "revoke", `{"reason":"late"}`); c != 409 {
		t.Fatalf("revoke expired = %d", c)
	}
	// an unswept expired grant does not block a new request
	m.grant(m.op, "15")
	m.advance(20 * time.Minute)
	if c, _ := m.grantReq(m.op, body("15")); c != 201 {
		t.Fatalf("request after unswept expiry = %d", c)
	}
	// undecided for 24 h -> expired
	m.advance(25 * time.Hour)
	m.s.sweep()
	var n int
	m.s.db.QueryRow(`SELECT count(*) FROM access_grant`).Scan(&n)
	if n != 3 || m.audits("access_grant.expire", "success") != 3 { // GRT-026: rows stay
		t.Fatalf("grants %d expire audits %d", n, m.audits("access_grant.expire", "success"))
	}
}

// GRT-012, GRT-013: re-check when the worker starts; expiry during an apply does not interrupt it.
func TestGRT012_013_WorkerRecheck(t *testing.T) {
	m := newM8(t)
	m.grant(m.op, "15")
	// expires while queued: failed grant_expired, nothing applied
	var stackID string
	m.s.db.QueryRow(`SELECT id FROM stack`).Scan(&stackID)
	mu := m.s.stackLock(stackID)
	mu.Lock()
	c, b := m.prodDeploy(m.op, "1")
	if c != 202 {
		t.Fatalf("%d %s", c, b)
	}
	var d map[string]any
	json.Unmarshal([]byte(b), &d)
	m.advance(16 * time.Minute)
	mu.Unlock()
	m.s.wg.Wait()
	got := m.get(d["id"].(string))
	if got["status"] != "failed" || got["error"] != "grant_expired" || len(m.drv.calls) != 0 {
		t.Fatalf("%v calls %v", got, m.drv.calls)
	}
	if m.audits("deployment.run", "failed") != 1 {
		t.Fatal("not audited")
	}
	// expires mid-apply: the rollout completes
	m.grant(m.op, "15")
	m.drv.gate = make(chan struct{})
	_, b = m.prodDeploy(m.op, "1")
	json.Unmarshal([]byte(b), &d)
	for i := 0; i < 200 && m.get(d["id"].(string))["status"] != "running"; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	m.advance(16 * time.Minute)
	close(m.drv.gate)
	m.s.wg.Wait()
	if got := m.get(d["id"].(string)); got["status"] != "succeeded" {
		t.Fatalf("%v", got)
	}
}

// GRT-016, GRT-020: break-glass deploys to prod audited as a bypass, and still cannot approve.
func TestGRT016_BreakGlassBypass(t *testing.T) {
	m := newM8(t)
	c, b := m.prodDeploy(m.admin, "1")
	if c != 202 || !strings.Contains(b, `"grant_bypass":"break_glass"`) {
		t.Fatalf("%d %s", c, b)
	}
	m.s.wg.Wait()
	if !m.auditHas("deployment.request", "break_glass") || !m.auditHas("deployment.run", "break_glass") {
		t.Fatal("bypass not audited")
	}
	_, g := m.grantReq(m.op, body("15"))
	if c, _ := m.act(m.admin, g["id"].(string), "approve", ""); c != 403 {
		t.Fatalf("break-glass approve = %d", c)
	}
}

// GRT-010, 015, 020: the approver of a prod deployment needs a grant too; tokens use the owner's grants and cannot approve grants.
func TestGRT015_TokensAndDeploymentApproval(t *testing.T) {
	m := newM8(t)
	plain, hash := auth.GenToken()
	syncTokens(t, m.env, auth.TokenDef{Name: "ci", OwnerSub: "oa", Role: "operator", SHA256: hash, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	tok := map[string]string{"Authorization": "Bearer " + plain}
	if c, _ := m.prodDeploy(tok, "1"); c != 403 {
		t.Fatalf("token without owner grant = %d", c)
	}
	m.grant(m.op, "60") // the owner's grant
	if c, b := m.prodDeploy(tok, "1"); c != 202 {
		t.Fatalf("token with owner grant = %d %s", c, b)
	}
	m.s.wg.Wait()
	_, g := m.grantReq(m.aa, body("60"))
	if c, _ := m.act(tok, g["id"].(string), "approve", ""); c != 403 {
		t.Fatalf("token approve = %d", c)
	}
	// protected-DB deployment needs a second admin who also holds a grant
	m.revision(pg)
	c, b := m.prodDeploy(m.op, "2")
	var d map[string]any
	json.Unmarshal([]byte(b), &d)
	if c != 202 || d["status"] != "pending_approval" {
		t.Fatalf("%d %s", c, b)
	}
	approve := func() int {
		return m.do("POST", "/api/v1/deployments/"+d["id"].(string)+"/approve", "", m.aa).Code
	}
	if approve() != 403 {
		t.Fatal("approver without grant")
	}
	m.act(m.ap, g["id"].(string), "approve", "")
	if approve() != 200 {
		t.Fatal("approver with grant")
	}
	m.s.wg.Wait()
	if got := m.get(d["id"].(string)); got["status"] != "succeeded" {
		t.Fatalf("%v", got)
	}
}

// 10 filter + R-8: the approver and requester are told, a third user is not; listing visibility.
func TestGRT_EventsAndListVisibility(t *testing.T) {
	m := newM8(t)
	sub, _, _ := m.s.bus.Subscribe(nil)
	id := m.grant(m.op, "60")
	var seen []string
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Type == "access_grant.updated" {
			seen = append(seen, ev.Data.(map[string]any)["status"].(string))
			for who, want := range map[string]bool{
				"requester": m.s.visible(&auth.Principal{ID: "usr_oa", Groups: []string{"oa"}}, ev),
				"approver":  m.s.visible(&auth.Principal{ID: "usr_ap", Groups: []string{"apr"}}, ev),
				"other-ns":  m.s.visible(&auth.Principal{ID: "usr_bp", Groups: []string{"bpr"}}, ev),
				"third":     m.s.visible(&auth.Principal{ID: "usr_zz", Groups: []string{"va"}}, ev),
			} {
				if want != (who == "requester" || who == "approver") {
					t.Errorf("%s visibility = %v", who, want)
				}
			}
		}
	}
	if strings.Join(seen, ",") != "requested,active" {
		t.Fatalf("events %v", seen)
	}
	list := func(h map[string]string, path string) string { return m.do("GET", path, "", h).Body.String() }
	for _, c := range []struct {
		h    map[string]string
		path string
		want bool
	}{
		{m.op, "/api/v1/access-grants", true}, {m.ap, "/api/v1/access-grants", true}, {m.admin, "/api/v1/access-grants", true},
		{m.bp, "/api/v1/access-grants", false}, {m.viewer, "/api/v1/access-grants", false}, {m.ap, "/api/v1/access-grants/mine", false},
		{m.op, "/api/v1/access-grants/mine", true}, {m.admin, "/api/v1/access-grants?status=expired", false},
		{m.op, "/api/v1/access-grants?status=active&namespace=a", true},
	} {
		if got := strings.Contains(list(c.h, c.path), id); got != c.want {
			t.Errorf("%s lists grant = %v want %v", c.path, got, c.want)
		}
	}
	if m.do("GET", "/api/v1/access-grants/"+id, "", m.bp).Code != 404 || m.do("GET", "/api/v1/access-grants/"+id, "", m.ap).Code != 200 {
		t.Fatal("get visibility")
	}
	if !strings.Contains(list(m.op, "/api/v1/me"), id) { // active_grants
		t.Fatal("/me active_grants")
	}
}

// GRT-040, GRT-041: signed webhooks for requested/approved/expired; retries then webhook.failed.
func TestGRT040_041_Webhook(t *testing.T) {
	m := newM8(t)
	var mu sync.Mutex
	var events []string
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(b)
		if r.Header.Get("X-C8-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("bad signature")
		}
		var p struct {
			Event string
			Grant map[string]any
		}
		json.Unmarshal(b, &p)
		mu.Lock()
		events = append(events, p.Event)
		mu.Unlock()
		if fail {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	m.s.cfg.WebhookURL, m.s.cfg.WebhookSecret = srv.URL, "s3cret"
	m.s.hookBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	m.grant(m.op, "15")
	m.advance(16 * time.Minute)
	m.s.sweep()
	m.s.wg.Wait()
	if strings.Join(events, ",") != "access_grant.requested,access_grant.approved,access_grant.expired" {
		t.Fatalf("%v", events)
	}
	fail, events = true, nil
	m.grantReq(m.op, body("15"))
	m.s.wg.Wait()
	if len(events) != 3 || m.audits("webhook.failed", "failed") != 1 { // 3 attempts, then audited
		t.Fatalf("attempts %d, failed audits %d", len(events), m.audits("webhook.failed", "failed"))
	}
}
