// Package targets parses C8_TARGETS, syncs it into the DB and checks reachability (K8S-001..003, D-49).
package targets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/KazzyAPI/control-8/internal/audit"
	"github.com/KazzyAPI/control-8/internal/auth"
	"github.com/KazzyAPI/control-8/internal/config"
)

// Spec is one C8_TARGETS entry; it holds references to credentials, never their content (K8S-001).
type Spec struct {
	ID             string `json:"-"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Tier           string `json:"environment_tier"`
	KubeAuth       string `json:"kube_auth"`
	KubeconfigEnv  string `json:"kubeconfig_env"`
	KubeconfigPath string `json:"kubeconfig_path"`
	KubeContext    string `json:"kube_context"`
}

var slug = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Parse validates the list; any error names C8_TARGETS (exit 2).
func Parse(raw []json.RawMessage) ([]Spec, error) {
	var out []Spec
	bad := func(i int, m string) error {
		return &config.Error{Var: "C8_TARGETS", Msg: fmt.Sprintf("entry %d: %s", i, m)}
	}
	for i, r := range raw {
		var s Spec
		d := json.NewDecoder(bytes.NewReader(r))
		d.DisallowUnknownFields()
		if err := d.Decode(&s); err != nil {
			return nil, bad(i, "invalid JSON object")
		}
		switch {
		case !slug.MatchString(s.Name):
			return nil, bad(i, "name must be a slug")
		case slices.ContainsFunc(out, func(o Spec) bool { return o.Name == s.Name }):
			return nil, bad(i, "duplicate name")
		case s.Kind == "docker":
			return nil, bad(i, "runtime_not_enabled")
		case s.Kind != "kubernetes":
			return nil, bad(i, "kind must be kubernetes")
		case s.Tier != "prod" && s.Tier != "non-prod":
			return nil, bad(i, "environment_tier must be prod|non-prod")
		case s.KubeAuth == "in_cluster" && (s.KubeconfigEnv != "" || s.KubeconfigPath != ""):
			return nil, bad(i, "in_cluster takes no kubeconfig reference")
		case s.KubeAuth == "kubeconfig" && (s.KubeconfigEnv == "") == (s.KubeconfigPath == ""):
			return nil, bad(i, "kubeconfig needs exactly one of kubeconfig_env, kubeconfig_path")
		case s.KubeAuth != "in_cluster" && s.KubeAuth != "kubeconfig":
			return nil, bad(i, "kube_auth must be in_cluster|kubeconfig")
		}
		out = append(out, s)
	}
	return out, nil
}

// Sync upserts specs by name, drops rows no longer configured, fills Spec.ID and audits changes (K8S-002).
// shortcut: removal is not blocked by stacks; the target_in_use exit 2 lands with the stack table in M6.
func Sync(db *sql.DB, specs []Spec) error {
	rows, err := db.Query(`SELECT name,id,environment_tier FROM target`)
	if err != nil {
		return err
	}
	type old struct{ id, tier string }
	have := map[string]old{}
	for rows.Next() {
		var n string
		var o old
		rows.Scan(&n, &o.id, &o.tier)
		have[n] = o
	}
	rows.Close()
	var added, removed []string
	changed := []map[string]string{}
	for i := range specs {
		s := &specs[i]
		cfg, _ := json.Marshal(map[string]string{"kube_auth": s.KubeAuth, "kubeconfig_env": s.KubeconfigEnv,
			"kubeconfig_path": s.KubeconfigPath, "kube_context": s.KubeContext})
		if o, ok := have[s.Name]; ok {
			s.ID = o.id
			if o.tier != s.Tier {
				changed = append(changed, map[string]string{"name": s.Name, "from": o.tier, "to": s.Tier})
			}
			_, err = db.Exec(`UPDATE target SET kind=?,environment_tier=?,config=? WHERE name=?`, s.Kind, s.Tier, string(cfg), s.Name)
		} else {
			s.ID = auth.NewID("tgt")
			added = append(added, s.Name)
			_, err = db.Exec(`INSERT INTO target(id,name,kind,environment_tier,config) VALUES(?,?,?,?,?)`, s.ID, s.Name, s.Kind, s.Tier, string(cfg))
		}
		if err != nil {
			return err
		}
		delete(have, s.Name)
	}
	for n := range have {
		removed = append(removed, n)
		if _, err := db.Exec(`DELETE FROM target WHERE name=?`, n); err != nil {
			return err
		}
	}
	slices.Sort(removed)
	if len(added)+len(removed)+len(changed) == 0 {
		return nil
	}
	return audit.Write(db, audit.Event{Action: "target.config_sync", TargetType: "target",
		Details: map[string]any{"added": nz(added), "removed": nz(removed), "tier_changed": changed}})
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Target is the API shape (openapi Target) and the target.status event payload.
type Target struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	Tier           string  `json:"environment_tier"`
	KubeAuth       *string `json:"kube_auth"`
	KubeconfigEnv  *string `json:"kubeconfig_env"`
	KubeconfigPath *string `json:"kubeconfig_path"`
	KubeContext    *string `json:"kube_context"`
	ServerVersion  *string `json:"server_version"`
	Reachable      bool    `json:"reachable"`
	Sync           string  `json:"sync"`
	LastError      *string `json:"last_error"`
	LastSeenAt     *string `json:"last_seen_at"`
}

func ref(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Versioner is the slice of the discovery client the checker needs (faked in tests).
type Versioner interface {
	ServerVersion() (*version.Info, error)
}

// Checker keeps per-target reachability and publishes a Target on every change (K8S-003, EVT-004).
type Checker struct {
	Build   func(Spec) (Versioner, error)
	Publish func(Target) // set by the API; may be nil
	Now     func() time.Time
	mu      sync.Mutex
	specs   []Spec
	st      map[string]*Target
	clients map[string]Versioner
}

func NewChecker(specs []Spec, build func(Spec) (Versioner, error)) *Checker {
	c := &Checker{Build: build, Now: time.Now, specs: specs, st: map[string]*Target{}, clients: map[string]Versioner{}}
	for _, s := range specs {
		c.st[s.Name] = &Target{ID: s.ID, Name: s.Name, Kind: s.Kind, Tier: s.Tier, KubeAuth: ref(s.KubeAuth),
			KubeconfigEnv: ref(s.KubeconfigEnv), KubeconfigPath: ref(s.KubeconfigPath), KubeContext: ref(s.KubeContext), Sync: "syncing"}
	}
	return c
}

// List returns a snapshot in config order.
func (c *Checker) List() []Target {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Target{}
	for _, s := range c.specs {
		out = append(out, *c.st[s.Name])
	}
	return out
}

func (c *Checker) Get(name string) (Target, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.st[name]; t != nil {
		return *t, true
	}
	return Target{}, false
}

// Check runs one reachability check for the named target.
// Build errors are reported as the fixed text kubeconfig_invalid so no credential detail can leak.
func (c *Checker) Check(name string) {
	c.mu.Lock()
	cl, spec := c.clients[name], Spec{}
	for _, s := range c.specs {
		if s.Name == name {
			spec = s
		}
	}
	c.mu.Unlock()
	var ver, errText string
	if cl == nil {
		var err error
		if cl, err = c.Build(spec); err != nil {
			errText = "kubeconfig_invalid"
		} else {
			c.mu.Lock()
			c.clients[name] = cl
			c.mu.Unlock()
		}
	}
	if errText == "" {
		if v, err := cl.ServerVersion(); err != nil {
			errText = redact(err)
		} else {
			ver = v.GitVersion
		}
	}
	c.mu.Lock()
	t := c.st[name]
	old := *t
	t.Reachable, t.Sync, t.LastError, t.ServerVersion = errText == "", "down", ref(errText), ref(ver)
	if t.Reachable {
		t.Sync = "syncing" // M9 replaces this with informer state
		now := c.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		t.LastSeenAt = &now
	}
	cur := *t
	c.mu.Unlock()
	if c.Publish != nil && !same(old, cur) {
		c.Publish(cur)
	}
}

func same(a, b Target) bool {
	return a.Reachable == b.Reachable && a.Sync == b.Sync && deref(a.LastError) == deref(b.LastError) &&
		deref(a.ServerVersion) == deref(b.ServerVersion)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func redact(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Run checks every target now and then every interval until ctx ends.
func (c *Checker) Run(ctx context.Context, every time.Duration) {
	for _, s := range c.specs {
		go func(n string) {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				c.Check(n)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}(s.Name)
	}
}

// BuildClient makes a discovery client from a spec: in-cluster, or kubeconfig content from the
// env var (or <VAR>_FILE) / mounted file. 10 s request timeout (K8S-003).
func BuildClient(getenv func(string) string, readFile func(string) ([]byte, error)) func(Spec) (Versioner, error) {
	return func(s Spec) (Versioner, error) {
		var cfg *rest.Config
		var err error
		if s.KubeAuth == "in_cluster" {
			cfg, err = rest.InClusterConfig()
		} else {
			data := []byte(getenv(s.KubeconfigEnv))
			switch {
			case s.KubeconfigPath != "":
				data, err = readFile(s.KubeconfigPath)
			case len(data) == 0 && getenv(s.KubeconfigEnv+"_FILE") != "":
				data, err = readFile(getenv(s.KubeconfigEnv + "_FILE"))
			}
			if err == nil && len(data) == 0 {
				err = errors.New("empty kubeconfig")
			}
			if err == nil {
				var kc *clientcmdapi.Config
				if kc, err = clientcmd.Load(data); err == nil {
					cfg, err = clientcmd.NewDefaultClientConfig(*kc, &clientcmd.ConfigOverrides{CurrentContext: s.KubeContext}).ClientConfig()
				}
			}
		}
		if err != nil {
			return nil, err
		}
		cfg.Timeout = 10 * time.Second
		return discovery.NewDiscoveryClientForConfig(cfg)
	}
}
