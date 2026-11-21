package targets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/version"

	"github.com/KazzyAPI/control-8/internal/store"
)

func raw(s ...string) (o []json.RawMessage) {
	for _, x := range s {
		o = append(o, json.RawMessage(x))
	}
	return
}

func TestK8S001_Parse(t *testing.T) {
	ok := `{"name":"home","kind":"kubernetes","environment_tier":"non-prod","kube_auth":"in_cluster"}`
	if sp, err := Parse(raw(ok)); err != nil || len(sp) != 1 {
		t.Fatalf("%v %v", sp, err)
	}
	for name, e := range map[string]string{
		"bad name":      `{"name":"A_B","kind":"kubernetes","environment_tier":"prod","kube_auth":"in_cluster"}`,
		"docker":        `{"name":"d","kind":"docker","environment_tier":"prod"}`,
		"tier":          `{"name":"d","kind":"kubernetes","environment_tier":"x","kube_auth":"in_cluster"}`,
		"both refs":     `{"name":"d","kind":"kubernetes","environment_tier":"prod","kube_auth":"kubeconfig","kubeconfig_env":"A","kubeconfig_path":"/p"}`,
		"no ref":        `{"name":"d","kind":"kubernetes","environment_tier":"prod","kube_auth":"kubeconfig"}`,
		"incluster+ref": `{"name":"d","kind":"kubernetes","environment_tier":"prod","kube_auth":"in_cluster","kubeconfig_env":"A"}`,
		"unknown key":   `{"name":"d","kind":"kubernetes","environment_tier":"prod","kube_auth":"in_cluster","kubeconfig":"CONTENT"}`,
		"duplicate":     ok + "," + ok,
	} {
		in := raw(e)
		if name == "duplicate" {
			in = raw(ok, ok)
		}
		if _, err := Parse(in); err == nil || !strings.Contains(err.Error(), "C8_TARGETS") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestK8S002_SyncAudit(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer db.Close()
	mk := func(tier string, names ...string) []Spec {
		var s []Spec
		for _, n := range names {
			s = append(s, Spec{Name: n, Kind: "kubernetes", Tier: tier, KubeAuth: "kubeconfig", KubeconfigEnv: "C8_KUBECONFIG_X"})
		}
		return s
	}
	a := mk("non-prod", "a", "b")
	if err := Sync(db, a); err != nil || !strings.HasPrefix(a[0].ID, "tgt_") {
		t.Fatalf("%v %+v", err, a)
	}
	id := a[0].ID
	b := mk("prod", "a")
	Sync(db, b)
	if b[0].ID != id {
		t.Fatal("id not stable")
	}
	Sync(db, b) // no change: no audit row
	var n int
	var det string
	db.QueryRow(`SELECT count(*) FROM audit_event WHERE action='target.config_sync' AND actor_type='system'`).Scan(&n)
	db.QueryRow(`SELECT details FROM audit_event WHERE action='target.config_sync' ORDER BY id DESC LIMIT 1`).Scan(&det)
	if n != 2 || !strings.Contains(det, `"removed":["b"]`) || !strings.Contains(det, `"from":"non-prod"`) {
		t.Fatalf("n=%d det=%s", n, det)
	}
	var cnt int
	var cfg string
	db.QueryRow(`SELECT count(*),max(config) FROM target`).Scan(&cnt, &cfg)
	if cnt != 1 || !strings.Contains(cfg, "C8_KUBECONFIG_X") {
		t.Fatal(cnt, cfg)
	}
}

type fakeV struct{ err error }

func (f *fakeV) ServerVersion() (*version.Info, error) {
	return &version.Info{GitVersion: "v1.31.0"}, f.err
}

// K8S-003 + EVT: reachability changes publish target.status once per change.
func TestK8S003_CheckPublishesOnChange(t *testing.T) {
	f := &fakeV{err: errors.New("dial tcp: refused")}
	c := NewChecker([]Spec{{ID: "tgt_1", Name: "t", Kind: "kubernetes", Tier: "prod", KubeAuth: "in_cluster"}},
		func(Spec) (Versioner, error) { return f, nil })
	var got []Target
	c.Publish = func(t Target) { got = append(got, t) }
	c.Check("t")
	c.Check("t") // unchanged: no event
	f.err = nil
	c.Check("t")
	c.Check("t") // only last_seen_at moves
	if len(got) != 2 || got[0].Reachable || *got[0].LastError == "" || got[0].Sync != "down" ||
		!got[1].Reachable || *got[1].ServerVersion != "v1.31.0" || got[1].LastError != nil {
		t.Fatalf("%+v", got)
	}
}

// K8S-001 / acceptance: garbage kubeconfig => reachable false + kubeconfig_invalid; content never surfaces.
func TestK8S001_KubeconfigInvalidNeverLeaks(t *testing.T) {
	const secret = "SUPER-SECRET-TOKEN-123"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "kc"), []byte("not: [a kubeconfig\n"+secret), 0o600)
	env := map[string]string{"C8_KUBECONFIG_ENV": "garbage " + secret}
	build := BuildClient(func(k string) string { return env[k] }, os.ReadFile)
	specs := []Spec{
		{ID: "1", Name: "env", Kind: "kubernetes", KubeAuth: "kubeconfig", KubeconfigEnv: "C8_KUBECONFIG_ENV"},
		{ID: "2", Name: "file", Kind: "kubernetes", KubeAuth: "kubeconfig", KubeconfigPath: filepath.Join(dir, "kc")},
		{ID: "3", Name: "missing", Kind: "kubernetes", KubeAuth: "kubeconfig", KubeconfigPath: filepath.Join(dir, "nope")},
		{ID: "4", Name: "unset", Kind: "kubernetes", KubeAuth: "kubeconfig", KubeconfigEnv: "C8_KUBECONFIG_UNSET"},
	}
	c := NewChecker(specs, build)
	var events []Target
	c.Publish = func(t Target) { events = append(events, t) }
	for _, s := range specs {
		c.Check(s.Name)
	}
	b, _ := json.Marshal(append(c.List(), events...))
	if strings.Contains(string(b), secret) {
		t.Fatal("kubeconfig content leaked")
	}
	for _, tg := range c.List() {
		if tg.Reachable || tg.LastError == nil || *tg.LastError != "kubeconfig_invalid" {
			t.Errorf("%s: %+v", tg.Name, tg)
		}
	}
	if len(events) != 4 {
		t.Fatalf("events %d", len(events))
	}
}

// K8S-002, DOM-011: removing a target that still has non-removed stacks fails before any change (main exits 2).
func TestK8S002_TargetInUse(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	defer db.Close()
	spec := func(n string) Spec {
		return Spec{Name: n, Kind: "kubernetes", Tier: "prod", KubeAuth: "kubeconfig", KubeconfigEnv: "C8_KUBECONFIG_X"}
	}
	a := []Spec{spec("a"), spec("b")}
	if err := Sync(db, a); err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO namespace(id,slug,display_name,environment_tier,created_at,updated_at) VALUES('ns_1','n','n','non-prod','t','t')`)
	db.Exec(`INSERT INTO asset(id,product_id,home_namespace_id,slug,name,created_at,updated_at) VALUES('ast_1','prd_unassigned','ns_1','w','w','t','t')`)
	db.Exec(`INSERT INTO stack(id,namespace_id,asset_id,target_id,slug,runtime,created_at,updated_at) VALUES('stk_1','ns_1','ast_1',?,'s','kubernetes','t','t')`, a[1].ID)
	err := Sync(db, []Spec{spec("a")})
	if !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("err = %v", err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM target`).Scan(&n)
	if n != 2 {
		t.Fatal("target removed despite stacks")
	}
	db.Exec(`UPDATE stack SET state='removed'`)
	if err := Sync(db, []Spec{spec("a")}); err != nil {
		t.Fatal(err)
	}
}
