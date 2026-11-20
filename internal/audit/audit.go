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
func Write(db *sql.DB, e Event) error {
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
	_, err := db.Exec(`INSERT INTO audit_event(at,actor_type,actor_id,actor_display,auth_method,namespace_id,action,target_type,target_id,outcome,details,request_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, store.Ts(time.Now()), e.ActorType, nz(e.ActorID), nz(e.ActorDisplay), nz(e.AuthMethod),
		nz(e.Namespace), e.Action, nz(e.TargetType), nz(e.TargetID), e.Outcome, string(d), nz(e.RequestID))
	return err
}
