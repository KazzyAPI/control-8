// Package audit writes the append-only audit log (DOM-008, DOM-009).
package audit

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/KazzyAPI/control-8/internal/store"
)

type Event struct {
	ActorType, ActorID, ActorDisplay, AuthMethod string // ActorType defaults to "system"
	Namespace, Action, TargetType, TargetID      string
	Outcome                                      string // defaults to "success"
	Details                                      map[string]any
	RequestID                                    string
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Write appends one event. Details must already be redacted by the caller (DOM-010).
func Write(db *sql.DB, e Event) error { _, err := Insert(db, e); return err }

// Insert is Write that also returns the AuditEvent API shape, for the audit.created event (10 catalogue).
func Insert(db *sql.DB, e Event) (map[string]any, error) {
	if e.ActorType == "" {
		e.ActorType = "system"
	}
	if e.Outcome == "" {
		e.Outcome = "success"
	}
	d, _ := json.Marshal(e.Details)
	if e.Details == nil {
		d = []byte("{}")
	}
	at := store.Ts(time.Now())
	res, err := db.Exec(`INSERT INTO audit_event(at,actor_type,actor_id,actor_display,auth_method,namespace_id,action,target_type,target_id,outcome,details,request_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, at, e.ActorType, nz(e.ActorID), nz(e.ActorDisplay), nz(e.AuthMethod),
		nz(e.Namespace), e.Action, nz(e.TargetType), nz(e.TargetID), e.Outcome, string(d), nz(e.RequestID))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	det := e.Details
	if det == nil {
		det = map[string]any{}
	}
	return map[string]any{"id": id, "at": at, "actor_type": e.ActorType, "actor_id": nz(e.ActorID), "actor_display": nz(e.ActorDisplay),
		"auth_method": nz(e.AuthMethod), "namespace": nz(e.Namespace), "action": e.Action, "target_type": nz(e.TargetType),
		"target_id": nz(e.TargetID), "outcome": e.Outcome, "details": det, "request_id": nz(e.RequestID)}, nil
}
