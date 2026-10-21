// Package state defines transaction-local isolation for execution runtimes.
package state

import "context"

// Checkpoint is an opaque journal-instance token. Clients must not inspect or
// fabricate tokens. Tokens are not consensus data and must never affect chain
// decisions. Only the newest outstanding checkpoint can be reverted/released.
type Checkpoint string

// Changes are relative to the original baseline: no duplicate deletions and no
// key in both sets. Values and snapshots are caller-owned, including nil writes.
type Changes struct {
	Writes  map[string][]byte
	Deletes []string
}

// Reader is the minimal read capability needed by an execution journal. Values
// returned to the caller are owned by that caller. An absent key differs from a
// present nil or empty value. A borrowed baseline must remain stable for the
// journal's lifetime; the journal neither closes nor mutates it.
type Reader interface {
	Get(context.Context, string) ([]byte, bool, error)
}

// Factory creates isolated journals over borrowed baselines. Creation performs
// no baseline reads, takes no ownership and starts no background work.
type Factory interface {
	New(context.Context, Reader) (Journal, error)
}

// Journal is isolated from durable persistence. Revert consumes a same-instance
// LIFO checkpoint, restoring writes and deletions; stale/foreign tokens error
// without modifying state or the checkpoint stack.
// Get distinguishes an absent key from a present empty value. Changes returns
// a snapshot without committing, clearing or publishing mutations. A journal
// is confined to one execution; concurrency is not assumed.
type Journal interface {
	Reader
	Set(context.Context, string, []byte) error
	Delete(context.Context, string) error
	Checkpoint(context.Context) (Checkpoint, error)
	Revert(context.Context, Checkpoint) error
	// Release consumes the newest checkpoint while retaining its changes. It
	// commits nothing durably; an enclosing checkpoint can still roll them back.
	Release(context.Context, Checkpoint) error
	Changes(context.Context) (Changes, error)
}
