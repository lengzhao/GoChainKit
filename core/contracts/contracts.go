// Package contracts defines plugin boundaries independently of PluginKit.
// These draft interfaces are intended for the single-node development chain.
package contracts

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core/types"
)

var (
	ErrFull               = errors.New("capacity exceeded")
	ErrConflict           = errors.New("parent head conflict")
	ErrAlreadyCommitted   = errors.New("already committed")
	ErrClosed             = errors.New("component closed")
	ErrNotFound           = errors.New("not found")
	ErrInvalidTransaction = errors.New("invalid transaction")
	ErrCommitUnknown      = errors.New("commit outcome unknown; query head")
)

// Lifecycle resources have a single owner. Stop is idempotent and waits for
// background work to exit. Restart requires a new instance.
type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}
type Node interface{ Lifecycle }

// Mempool is concurrency-safe, owns copies of accepted transactions, and returns
// copies. Select is non-destructive and preserves admission order within budget.
// Add is idempotent for an identical transaction. Remove is idempotent.
type Mempool interface {
	Add(context.Context, types.Transaction) error
	Select(context.Context, int) ([]types.Transaction, error)
	Remove(context.Context, []types.Hash) error
}

// StateReader reads an immutable snapshot, returns owned bytes, and reports
// missing keys using ErrNotFound. Its owner releases the snapshot with Close.
type StateReader interface {
	Get(context.Context, []byte) ([]byte, error)
	// Entries returns a caller-owned full snapshot for the minimal state-root
	// algorithm. Production backends may later replace this with bounded iteration.
	Entries(context.Context) (map[string][]byte, error)
	Root() types.Hash
	Close() error
}
type StateStore interface {
	Snapshot(context.Context, types.Hash) (StateReader, error)
}

// Executor must not mutate parent or block. Identical input produces identical
// output; invalid transactions reject the whole candidate without side effects.
type Executor interface {
	Execute(context.Context, StateReader, types.Block) (types.ExecutionResult, error)
}

// Committer atomically checks expectedHead and commits all canonical data.
// A repeated matching block returns ErrAlreadyCommitted. ErrCommitUnknown
// requires querying durable data before retrying; cancellation is not rollback.
type Committer interface {
	Commit(context.Context, types.Hash, types.Block, types.ExecutionResult) error
}
type ChainStore interface {
	Head(context.Context) (types.Hash, error)
	Block(context.Context, types.Hash) (types.Block, error)
	Receipt(context.Context, types.Hash) (types.Receipt, error)
}

// BlockBuilder constructs an isolated candidate against the supplied head.
type BlockBuilder interface {
	Build(context.Context, types.Hash) (types.Block, types.ExecutionResult, error)
}

// ConsensusHost is implemented by the coordinator; validators independently
// execute proposals. Commit is the only finality callback permitted to persist.
type ConsensusHost interface {
	Propose(context.Context) (types.Block, error)
	Validate(context.Context, types.Block) error
	Commit(context.Context, types.Block) error
}

// Consensus is single-owner; Run blocks until canceled or failed and must not
// silently discard finality errors. Future network protocols may extend it.
type Consensus interface {
	Run(context.Context, ConsensusHost) error
}

type Message struct {
	Peer     string
	Protocol string
	Payload  []byte
}

// Transport bounds message sizes, copies retained data and stops on cancellation.
// Run delivers owned inbound data synchronously to handle; callbacks must finish.
type Transport interface {
	Run(context.Context, func(context.Context, Message) error) error
	Broadcast(context.Context, Message) error
}
type API interface{ Lifecycle }

// Crypto implementations must validate lengths and return errors, never panic
// for untrusted input. Algorithm choice is a protocol parameter.
type Crypto interface {
	Hash([]byte) types.Hash
	Verify(publicKey, message, signature []byte) error
}
