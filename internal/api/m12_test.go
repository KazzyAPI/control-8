package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/digest"
)

type m12 struct {
	*m10
	srv  *httptest.Server
	host string
	p    *digest.Poller
	d1   string
}

func (m *m12) push() string {
	img, _ := random.Image(64, 1)
	if err := crane.Push(img, m.host+"/app:1"); err != nil {
		m.t.Fatal(err)
	}
	d, _ := img.Digest()
	return d.String()
}

// newM12: in-process registry with app:1, a deployed managed_rollout revision 1 pinned to that digest.
func newM12(t *testing.T, poll bool) *m12 {
	m := &m12{m10: newM10(t)}
	m.srv = httptest.NewServer(registry.New())
	t.Cleanup(m.srv.Close)
	m.host = strings.TrimPrefix(m.srv.URL, "http://")
	m.d1 = m.push()
	kc, _ := digest.ParseAuth("")
	m.p = &digest.Poller{DB: m.s.db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Resolve: kc.Resolver()}
	WithDigest(m.p)(m.s)
	manifests := strings.Replace(web3, "image: busybox", "image: "+m.host+"/app:1@"+m.d1, 1)
	b, _ := json.Marshal(map[string]any{"safety_profile": "managed_rollout", "manifests": manifests, "rollout_timeout_seconds": 30, "digest_poll": poll})
	if w := m.do("POST", "/api/v1/namespaces/a/stacks/s1/revisions", string(b), m.op); w.Code != 201 {
		t.Fatal(w.Body)
	}
	m.script("ok")
	if d := m.deployRev(1, "managed_rollout"); d["status"] != "succeeded" {
		t.Fatal(d)
	}
	return m
}

func (m *m12) poll() { m.p.Poll(context.Background()); m.s.wg.Wait() }

func (m *m12) count(q string, a ...any) (n int) {
	m.s.db.QueryRow(q, a...).Scan(&n)
	return
}

func (m *m12) digestStatus() map[string]any {
	st, _ := m.stack()["digest_status"].(map[string]any)
	return st
}

// K8S-051, K8S-052, D-46, DOM-009, R-8: a moved tag on a non-prod stack becomes an image update by system
// (digest_poll trigger, pinned to the new digest), audited, and a viewer gets stack.updated with the update.
func TestK8S051_NonProdAutoUpdate(t *testing.T) {
	m := newM12(t, true)
	sub, _, _ := m.s.bus.Subscribe(nil)
	d2 := m.push()
	m.script("ok")
	m.poll()
	var trig, by, status string
	m.s.db.QueryRow(`SELECT trigger,requested_by,status FROM deployment WHERE trigger='digest_poll'`).Scan(&trig, &by, &status)
	if by != "system" || status != "succeeded" || m.stack()["desired_revision"] != float64(2) {
		t.Fatal(by, status, m.stack())
	}
	if m.count(`SELECT count(*) FROM revision WHERE number=2 AND origin='image_update' AND created_by='system' AND instr(manifests_input,?)>0`, m.host+"/app:1@"+d2) != 1 {
		t.Fatal("revision 2 not pinned to the new digest")
	}
	if m.count(`SELECT count(*) FROM audit_event WHERE action='digest_poll.detected' AND actor_type='system' AND details LIKE ?`, "%"+d2+"%") != 1 ||
		m.count(`SELECT count(*) FROM audit_event WHERE action='image_update.request' AND actor_type='system'`) != 1 {
		t.Fatal("audit")
	}
	var saw bool
	for len(sub.C) > 0 {
		ev := <-sub.C
		if ev.Type == "stack.updated" && strings.Contains(mustJSON(ev.Data), d2) {
			saw = true
			if !m.s.visible(&auth.Principal{ID: "u", Groups: []string{"va"}}, ev) || m.s.visible(&auth.Principal{ID: "u2"}, ev) {
				t.Fatal("stack.updated filter")
			}
		}
	}
	if !saw {
		t.Fatal("no stack.updated carrying the update")
	}
	m.poll() // converged: nothing more to do
	if m.count(`SELECT count(*) FROM deployment WHERE trigger='digest_poll'`) != 1 || len(m.digestStatus()["updates"].([]any)) != 0 {
		t.Fatal(m.digestStatus())
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// D-46, K8S-051: prod is report-only: digest_status.updates + audit, never a revision or deployment.
func TestK8S051_ProdReportsOnly(t *testing.T) {
	m := newM12(t, true)
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	d2 := m.push()
	m.poll()
	ups, _ := m.digestStatus()["updates"].([]any)
	if len(ups) != 1 || ups[0].(map[string]any)["available"] != d2 || ups[0].(map[string]any)["current"] != m.d1 || ups[0].(map[string]any)["workload"] != "Deployment/web" {
		t.Fatal(m.digestStatus())
	}
	if m.count(`SELECT count(*) FROM revision`) != 1 || m.count(`SELECT count(*) FROM deployment WHERE trigger='digest_poll'`) != 0 ||
		m.count(`SELECT count(*) FROM audit_event WHERE action='digest_poll.detected'`) != 1 {
		t.Fatal("prod must only report")
	}
	m.poll() // same update seen again: no second detected event
	if m.count(`SELECT count(*) FROM audit_event WHERE action='digest_poll.detected'`) != 1 {
		t.Fatal("detected repeated")
	}
}

// K8S-051: a digest whose update failed once is not requested again.
func TestK8S051_FailedDigestNotRetried(t *testing.T) {
	m := newM12(t, true)
	m.push()
	m.script("bad", "ok") // new revision unhealthy, rolled back
	m.poll()
	if d := m.stack(); d["desired_revision"] != float64(1) || m.count(`SELECT count(*) FROM deployment WHERE trigger='digest_poll' AND status='failed'`) != 1 {
		t.Fatal(d)
	}
	m.poll()
	m.poll()
	if m.count(`SELECT count(*) FROM deployment WHERE trigger='digest_poll'`) != 1 || m.count(`SELECT count(*) FROM revision`) != 2 {
		t.Fatal("retried a failed digest")
	}
}

// K8S-051, 08 failure modes: registry down -> digest_status.error, no update, recovers on a later tick.
func TestK8S051_RegistryDown(t *testing.T) {
	m := newM12(t, true)
	m.push()
	m.srv.Close()
	m.poll()
	st := m.digestStatus()
	if e, _ := st["error"].(string); e == "" || len(st["updates"].([]any)) != 0 || m.count(`SELECT count(*) FROM deployment WHERE trigger='digest_poll'`) != 0 {
		t.Fatal(st)
	}
}

// K8S-051: opt-in per revision; skipped while frozen or while a deployment is open.
func TestK8S051_SkipRules(t *testing.T) {
	m := newM12(t, false)
	m.push()
	m.poll()
	if m.stack()["digest_status"] != nil || m.count(`SELECT count(*) FROM revision`) != 1 {
		t.Fatal("polled without opt-in")
	}
	m.s.db.Exec(`UPDATE revision SET digest_poll=1`)
	m.s.db.Exec(`UPDATE stack SET rollout_frozen=1`)
	m.poll()
	if m.stack()["digest_status"] != nil {
		t.Fatal("polled a frozen stack")
	}
	m.s.db.Exec(`UPDATE stack SET rollout_frozen=0`)
	m.s.db.Exec(`UPDATE deployment SET status='running',finished_at=NULL WHERE rowid=(SELECT max(rowid) FROM deployment)`)
	m.poll()
	if m.stack()["digest_status"] != nil {
		t.Fatal("polled with a deployment open")
	}
}

// K8S-051: Run polls once at start and then once per tick of the (fake) clock.
func TestK8S051_RunTicks(t *testing.T) {
	m := newM12(t, true)
	m.s.db.Exec(`UPDATE target SET environment_tier='prod'`)
	beat := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.p.Run(ctx, time.Minute, func(time.Duration) (<-chan time.Time, func()) { return beat, func() {} })
		close(done)
	}()
	waitFor(t, "first poll", func() bool { return m.digestStatus() != nil })
	m.s.db.Exec(`UPDATE stack SET digest_status=NULL`)
	beat <- time.Time{}
	waitFor(t, "poll on tick", func() bool { return m.digestStatus() != nil })
	cancel()
	<-done
}
