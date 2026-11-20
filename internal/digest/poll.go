// Package digest is the opt-in registry digest poll (K8S-051, D-46).
package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	k8s "github.com/KazzyAPI/control-8/internal/runtime/kubernetes"
	"github.com/KazzyAPI/control-8/internal/store"
)

// Poller checks stacks whose desired revision has digest_poll=true. The api wires Request, Audit and Changed.
type Poller struct {
	DB      *sql.DB
	Log     *slog.Logger
	Now     func() time.Time
	Resolve func(ctx context.Context, ref string) (string, error)
	// Request asks for an image update as actor system (non-prod only).
	Request func(stackID, workload, container, image string) error
	Audit   func(ns, stackID string, details map[string]any)
	Changed func(stackID string)
	Redact  func(string) string
}

type update struct {
	Workload  string  `json:"workload"`
	Container string  `json:"container"`
	Current   *string `json:"current"`
	Available string  `json:"available"`
}

type status struct {
	CheckedAt string   `json:"checked_at"`
	Error     *string  `json:"error"`
	Updates   []update `json:"updates"`
}

var sha = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// parseRef splits "repo:tag[@sha256:x]"; ok=false when there is no tag to follow (digest-only refs are skipped).
func parseRef(img string) (repo, tag, pinned string, ok bool) {
	img, pinned, _ = strings.Cut(img, "@")
	repo = img
	if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
		repo, tag = img[:i], img[i+1:]
	} else if pinned == "" {
		tag = "latest"
	}
	return repo, tag, pinned, tag != ""
}

// Run polls now and then every interval until ctx ends. tick is the clock (a fake in tests).
func (p *Poller) Run(ctx context.Context, every time.Duration, tick func(time.Duration) (<-chan time.Time, func())) {
	c, stop := tick(every)
	defer stop()
	for {
		p.Poll(ctx)
		select {
		case <-c:
		case <-ctx.Done():
			return
		}
	}
}

// Poll runs one pass over every eligible stack.
func (p *Poller) Poll(ctx context.Context) {
	rows, err := p.DB.Query(`SELECT st.id,n.slug,st.slug,st.desired_revision,COALESCE(st.digest_status,''),
		CASE WHEN n.environment_tier='prod' OR t.environment_tier='prod' THEN 1 ELSE 0 END
		FROM stack st JOIN namespace n ON n.id=st.namespace_id LEFT JOIN target t ON t.id=st.target_id
		WHERE st.runtime='kubernetes' AND st.state='active' AND st.safety_profile='managed_rollout' AND st.rollout_frozen=0
		AND st.desired_revision IS NOT NULL
		AND EXISTS(SELECT 1 FROM revision r WHERE r.stack_id=st.id AND r.number=st.desired_revision AND r.digest_poll=1)
		AND NOT EXISTS(SELECT 1 FROM deployment d WHERE d.stack_id=st.id AND d.status IN ('queued','running','pending_approval'))`)
	if err != nil {
		p.Log.Error("digest poll query", "err", err)
		return
	}
	type stk struct {
		id, ns, slug, prev string
		rev                int
		prod               bool
	}
	var all []stk
	for rows.Next() {
		var x stk
		rows.Scan(&x.id, &x.ns, &x.slug, &x.rev, &x.prev, &x.prod)
		all = append(all, x)
	}
	rows.Close()
	cache := map[string]struct {
		d   string
		err error
	}{}
	resolve := func(ref string) (string, error) {
		if c, ok := cache[ref]; ok {
			return c.d, c.err
		}
		d, err := p.Resolve(ctx, ref)
		cache[ref] = struct {
			d   string
			err error
		}{d, err}
		return d, err
	}
	for _, x := range all {
		if ctx.Err() != nil {
			return
		}
		ups, err := p.check(x.id, x.rev, resolve)
		st := status{CheckedAt: store.Ts(p.Now()), Updates: ups}
		if err != nil {
			msg := err.Error()
			if p.Redact != nil {
				msg = p.Redact(msg)
			}
			if len(msg) > 300 {
				msg = msg[:300]
			}
			st.Error, st.Updates = &msg, []update{}
			p.Log.Warn("digest poll", "stack", x.slug, "err", msg)
		}
		if st.Updates == nil {
			st.Updates = []update{}
		}
		var prev status
		json.Unmarshal([]byte(x.prev), &prev)
		b, _ := json.Marshal(st)
		p.DB.Exec(`UPDATE stack SET digest_status=? WHERE id=?`, string(b), x.id)
		for _, u := range st.Updates {
			if !hasUpdate(prev.Updates, u) && p.Audit != nil {
				p.Audit(x.ns, x.id, map[string]any{"stack": x.slug, "workload": u.Workload, "container": u.Container, "old": u.Current, "new": u.Available})
			}
		}
		if p.Changed != nil {
			p.Changed(x.id)
		}
		if x.prod || st.Error != nil { // D-46: prod only reports; an error never updates
			continue
		}
		for _, u := range st.Updates {
			if !p.failedBefore(x.id, u.Available) {
				if err := p.Request(x.id, u.Workload, u.Container, u.imageRef(p, x.id, x.rev)); err != nil {
					p.Log.Warn("digest poll update refused", "stack", x.slug, "err", err)
				}
				break // one open deployment per stack; the next update goes on a later tick
			}
		}
	}
}

func hasUpdate(l []update, u update) bool {
	for _, v := range l {
		if v.Workload == u.Workload && v.Container == u.Container && v.Available == u.Available {
			return true
		}
	}
	return false
}

// failedBefore: an update to this digest already ended unsuccessfully for the stack (K8S-051).
func (p *Poller) failedBefore(stackID, digest string) bool {
	var n int
	p.DB.QueryRow(`SELECT count(*) FROM deployment d JOIN revision r ON r.id=d.revision_id WHERE d.stack_id=? AND d.trigger='digest_poll'
		AND d.status IN ('failed','rejected','cancelled') AND instr(r.manifests_input,?)>0`, stackID, "@"+digest).Scan(&n)
	return n > 0
}

// imageRef is repo:tag@sha256:new for the container's current image.
func (u update) imageRef(p *Poller, stackID string, rev int) string {
	var input string
	p.DB.QueryRow(`SELECT manifests_input FROM revision WHERE stack_id=? AND number=?`, stackID, rev).Scan(&input)
	objs, _ := k8s.Parse(input)
	for _, o := range objs {
		if k8s.Name(o) == u.Workload {
			for _, c := range k8s.Containers(o) {
				if c["name"] == u.Container {
					img, _ := c["image"].(string)
					repo, tag, _, _ := parseRef(img)
					return repo + ":" + tag + "@" + u.Available
				}
			}
		}
	}
	return ""
}

// check resolves every followed container of the desired revision and returns those whose digest moved.
func (p *Poller) check(stackID string, rev int, resolve func(string) (string, error)) ([]update, error) {
	var input string
	p.DB.QueryRow(`SELECT manifests_input FROM revision WHERE stack_id=? AND number=?`, stackID, rev).Scan(&input)
	objs, err := k8s.Parse(input)
	if err != nil {
		return nil, err
	}
	var out []update
	for _, o := range objs {
		name, cs := k8s.Name(o), k8s.Containers(o)
		for _, c := range cs {
			img, _ := c["image"].(string)
			repo, tag, pinned, ok := parseRef(img)
			if !ok {
				continue
			}
			var cur *string
			if pinned != "" {
				cur = &pinned
			} else if len(cs) == 1 { // imageID is per workload, so only a single-container workload can be compared
				var id sql.NullString
				p.DB.QueryRow(`SELECT image_id FROM service WHERE stack_id=? AND name=?`, stackID, name).Scan(&id)
				if d := sha.FindString(id.String); d != "" {
					cur = &d
				}
			}
			if cur == nil {
				continue
			}
			avail, err := resolve(repo + ":" + tag)
			if err != nil {
				return nil, err
			}
			if avail != *cur {
				out = append(out, update{name, c["name"].(string), cur, avail})
			}
		}
	}
	return out, nil
}
