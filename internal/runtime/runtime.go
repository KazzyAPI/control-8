// Package runtime is the driver interface the deploy flow talks to (D-30). Only drivers write to a target.
package runtime

import (
	"context"
	"fmt"
	"time"
)

// Stack identifies the stack and where it lives (Kubernetes: the mapped namespace).
type Stack struct{ ID, Namespace string }

// Plan is what one revision applies: the rendered objects (K8S-026).
type Plan struct {
	Stack   Stack
	Objects []map[string]any
}

// Driver is one runtime backend. Precheck must not write anything the stack does not already own.
type Driver interface {
	Precheck(ctx context.Context, p Plan) error
	Apply(ctx context.Context, p Plan) (output string, err error)
	// Prune deletes the stack's objects that are not in the plan; it never deletes PVCs (D-37).
	Prune(ctx context.Context, p Plan) (output string, err error)
	// Teardown deletes everything the stack owns except PVCs, and never the namespace (D-35, D-37).
	Teardown(ctx context.Context, s Stack) (output string, err error)
	// Watch observes the target until ctx ends and calls Enqueue(stackID) on every change (K8S-060, 061).
	Watch(ctx context.Context, o WatchOptions) (Cache, error)
}

// WatchOptions: Resync is the informer resync; Poll is the list interval of the fallback (K8S-064).
type WatchOptions struct {
	Resync, Poll time.Duration
	Enqueue      func(stackID string)
}

// Cache is the observed state held by the informers; reading it never calls the API server (K8S-061).
type Cache interface {
	// Objects returns the cached workloads and pods labelled with the stack id (read-only).
	Objects(stackID string) []map[string]any
	// Sync is "watching", or "polling" when any kind falls back to list polling.
	Sync() string
}

// Error carries the stable machine code stored in deployment.error (ownership_conflict, argo_managed, ...).
type Error struct{ Code, Detail string }

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Detail) }
