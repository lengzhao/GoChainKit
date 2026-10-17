// Package state defines transaction-local isolation for execution runtimes.
package state

import "context"

// Checkpoint is an opaque journal-instance token. Clients must not inspect or
// fabricate tokens. Only the newest outstanding checkpoint can be reverted.
type Checkpoint string

// Changes are relative to the original baseline: no duplicate deletions and no
// key in both sets. Values and snapshots are caller-owned, including nil writes.
type Changes struct {
	Writes  map[string][]byte
	Deletes []string
}

// Journal is isolated from durable persistence. Revert consumes a same-instance
// LIFO checkpoint, restoring writes and deletions; stale/foreign tokens error
// without modifying state or the checkpoint stack.
// Get distinguishes an absent key from a present empty value. Changes returns
// a snapshot without committing, clearing or publishing mutations. A journal
// is confined to one execution; concurrency is not assumed.
type Journal interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte) error
	Delete(context.Context, string) error
	Checkpoint(context.Context) (Checkpoint, error)
	Revert(context.Context, Checkpoint) error
	// Release consumes the newest checkpoint while retaining its changes. It
	// commits nothing durably; an enclosing checkpoint can still roll them back.
	Release(context.Context, Checkpoint) error
	Changes(context.Context) (Changes, error)
}
