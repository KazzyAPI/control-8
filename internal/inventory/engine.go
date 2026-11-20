package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/KazzyAPI/control-8/internal/auth"
	rt "github.com/KazzyAPI/control-8/internal/runtime"
	"github.com/KazzyAPI/control-8/internal/store"
	"github.com/KazzyAPI/control-8/internal/targets"
)

// Engine debounces per stack, recomputes from the informer caches and writes service rows, drift and health (K8S-061).
type Engine struct {
	DB       *sql.DB
	Debounce time.Duration
	Changed  func(stackID string) // called after the DB write, only when something changed
	mu       sync.Mutex
	caches   map[string]rt.Cache // by target name
	timers   map[string]*time.Timer
	last     map[string]string // fingerprint of the last write per stack
	stopped  bool
}

func New(db *sql.DB, debounce time.Duration) *Engine {
	return &Engine{DB: db, Debounce: debounce, caches: map[string]rt.Cache{}, timers: map[string]*time.Timer{}, last: map[string]string{}}
}

// SetCache installs (or, with nil, drops) the cache of a target.
func (e *Engine) SetCache(target string, c rt.Cache) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c == nil {
		delete(e.caches, target)
	} else {
		e.caches[target] = c
	}
}

// Enqueue schedules a recompute of the stack; further calls inside the debounce window coalesce.
func (e *Engine) Enqueue(stackID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped || e.timers[stackID] != nil {
		return
	}
	e.timers[stackID] = time.AfterFunc(e.Debounce, func() {
		e.mu.Lock()
		delete(e.timers, stackID)
		stopped := e.stopped
		e.mu.Unlock()
		if !stopped {
			e.Recompute(stackID)
		}
	})
}

// Stop cancels pending recomputes.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	for _, t := range e.timers {
		t.Stop()
	}
}

func (e *Engine) stackIDs(q string, args ...any) (ids []string) {
	rows, err := e.DB.Query(q, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return
}

// EnqueueTarget recomputes every observable stack of a target (after (re)start of its informers).
func (e *Engine) EnqueueTarget(target string) {
	for _, id := range e.stackIDs(`SELECT st.id FROM stack st JOIN target t ON t.id=st.target_id WHERE t.name=? AND st.runtime='kubernetes' AND st.state IN ('active','failed')`, target) {
		e.Enqueue(id)
	}
}

// MarkUnknown sets health unknown on the target's stacks and keeps their last services and drift (K8S-065).
func (e *Engine) MarkUnknown(target string) {
	for _, id := range e.stackIDs(`UPDATE stack SET health='unknown' WHERE health!='unknown' AND runtime='kubernetes'
		AND target_id=(SELECT id FROM target WHERE name=?) RETURNING id`, target) {
		e.mu.Lock()
		delete(e.last, id)
		e.mu.Unlock()
		if e.Changed != nil {
			e.Changed(id)
		}
	}
}

// Recompute reads the stack's cached objects and rewrites its observed state; it publishes only on change.
func (e *Engine) Recompute(stackID string) {
	var tname string
	var desired int
	var frozen bool
	if e.DB.QueryRow(`SELECT t.name,st.desired_revision,st.rollout_frozen FROM stack st JOIN target t ON t.id=st.target_id
		WHERE st.id=? AND st.runtime='kubernetes' AND st.state IN ('active','failed') AND st.desired_revision IS NOT NULL`, stackID).Scan(&tname, &desired, &frozen) != nil {
		return
	}
	e.mu.Lock()
	c := e.caches[tname]
	e.mu.Unlock()
	if c == nil {
		return
	}
	var inv, rendered, dbs string
	var running bool
	if e.DB.QueryRow(`SELECT inventory,rendered,database_services,EXISTS(SELECT 1 FROM deployment WHERE stack_id=? AND status IN ('queued','running'))
		FROM revision WHERE stack_id=? AND number=?`, stackID, stackID, desired).Scan(&inv, &rendered, &dbs, &running) != nil {
		return
	}
	in := Input{Revision: desired, Frozen: frozen, Progressing: running, Cached: c.Objects(stackID)}
	json.Unmarshal([]byte(inv), &in.Inventory)
	json.Unmarshal([]byte(rendered), &in.Rendered)
	json.Unmarshal([]byte(dbs), &in.Databases)
	res := Compute(in)

	fp, _ := json.Marshal(res)
	e.mu.Lock()
	same := e.last[stackID] == string(fp)
	e.last[stackID] = string(fp)
	e.mu.Unlock()
	now := store.Ts(time.Now())
	if same {
		e.DB.Exec(`UPDATE stack SET observed_at=? WHERE id=?`, now, stackID)
		return
	}
	if err := e.write(stackID, res, now); err != nil {
		slog.Error("inventory write", "stack", stackID, "err", err)
		return
	}
	if e.Changed != nil {
		e.Changed(stackID)
	}
}

func (e *Engine) write(id string, r Result, now string) error {
	tx, err := e.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM service WHERE stack_id=?`, id); err != nil {
		return err
	}
	for _, s := range r.Services {
		ctr, _ := json.Marshal(s.Containers)
		if _, err := tx.Exec(`INSERT INTO service(id,stack_id,name,kind,desired_replicas,ready_replicas,image_ref,image_id,state,health,is_database,containers,observed_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, auth.NewID("svc"), id, s.Name, s.Kind, s.Desired, s.Ready, s.ImageRef, s.ImageID, s.State, s.Health, s.IsDatabase, string(ctr), now); err != nil {
			return err
		}
	}
	drift, _ := json.Marshal(r.Drift)
	if _, err := tx.Exec(`UPDATE stack SET drift=?,health=?,observed_at=? WHERE id=?`, string(drift), r.Health, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Manager runs the informers of every target: start when reachable, stop and mark stacks unknown when not,
// restart on recovery (K8S-060, 064, 065).
type Manager struct {
	E       *Engine
	Targets *targets.Checker
	Drivers func(target string) (rt.Driver, error)
	Resync  time.Duration
	Every   time.Duration // C8_TARGET_CHECK: reachability re-check and list-poll interval
}

func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range m.Targets.List() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sleep(ctx, 0) {
				if cur, _ := m.Targets.Get(t.Name); cur.Reachable {
					m.session(ctx, t.Name)
				}
				if !sleep(ctx, m.Every) {
					return
				}
			}
		}()
	}
	wg.Wait()
	m.E.Stop()
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (m *Manager) session(ctx context.Context, name string) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d, err := m.Drivers(name)
	if err != nil {
		return
	}
	c, err := d.Watch(sctx, rt.WatchOptions{Resync: m.Resync, Poll: m.Every, Enqueue: m.E.Enqueue})
	if err != nil {
		slog.Warn("informer start failed", "target", name, "err", err)
		return
	}
	m.E.SetCache(name, c)
	m.Targets.SetSync(name, c.Sync())
	m.E.EnqueueTarget(name)
	for sleep(sctx, m.Every) {
		if cur, _ := m.Targets.Get(name); !cur.Reachable {
			break
		}
	}
	m.E.SetCache(name, nil)
	if ctx.Err() == nil {
		m.E.MarkUnknown(name)
	}
}
