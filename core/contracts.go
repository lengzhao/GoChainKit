// Package core defines format-independent blockchain plugin core.
package core

import (
	"context"
	"errors"
	"fmt"
)

// ID is an opaque immutable byte string, not necessarily a 32-byte hash or hex.
// Protocols must produce stable, nonempty IDs; display encodings are API concerns.
type ID string
type Commitment string
type ProtocolDescriptor struct {
	Name    string
	Version string
	Network string
}

func (d ProtocolDescriptor) Validate() error {
	if d.Name == "" || d.Version == "" || d.Network == "" {
		return fmt.Errorf("protocol name, version and network required")
	}
	return nil
}

type ProtocolBound interface{ Protocol() ProtocolDescriptor }

func RequireCompatible(p ProtocolDescriptor, components ...ProtocolBound) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, c := range components {
		if c == nil || c.Protocol() != p {
			return fmt.Errorf("incompatible protocol dependency")
		}
	}
	return nil
}

// Transaction and Block are immutable. No header, signature algorithm, account
// model, ancestry topology or transaction layout is mandated here.
type Transaction interface{ ID() ID }
type Block interface{ ID() ID }

// Receipt carries protocol bytes, aggregated to at most one receipt per tx.
// Protocols may omit receipts. Returned data is caller-owned.
type Receipt struct {
	TransactionID ID
	Data          []byte
}
type ConfirmedTransaction struct {
	Transaction Transaction
	BlockID     ID
	Index       uint64
}
type ExecutionResult struct {
	Parent    ID
	BlockID   ID
	Writes    map[string][]byte
	Receipts  []Receipt
	StateRoot Commitment
}

// TransactionCodec is deterministic and returns caller-owned objects and bytes.
// Decode must reject malformed input and preserve IDs on a canonical round trip.
type TransactionCodec interface {
	EncodeTransaction(Transaction) ([]byte, error)
	DecodeTransaction([]byte) (Transaction, error)
}
type TransactionValidator interface {
	ValidateTransaction(context.Context, Transaction) error
}
type BlockCodec interface {
	EncodeBlock(Block) ([]byte, error)
	DecodeBlock([]byte) (Block, error)
	Transactions(Block) ([]Transaction, error)
}
type BlockFactory interface {
	Genesis(context.Context) (Block, map[string][]byte, error)
	Draft(context.Context, Block, []Transaction) (Block, error)
	Finalize(context.Context, Block, ExecutionResult) (Block, error)
}
type BlockValidator interface {
	ValidateBlock(context.Context, ChainStore, Block, Block, ExecutionResult) error
}

// CandidateValidator checks execution-independent candidate binding before state
// execution. A candidate anchored to a different canonical parent returns
// ErrConflict. Malformed candidates and other protocol violations remain their
// own errors. No execution-result commitment is checked here; ValidateBlock
// remains mandatory after execution and storage still atomically rechecks head.
type CandidateValidator interface {
	ValidateCandidate(context.Context, ChainStore, Block, Block) error
}
type StateCommitter interface {
	StateCommitment(context.Context, map[string][]byte) (Commitment, error)
}

// Consumer-specific capability sets keep pure codecs reusable without exposing
// unrelated construction, validation or execution services to every component.
type TransactionProtocol interface {
	ProtocolBound
	TransactionCodec
	TransactionValidator
}
type BuilderProtocol interface {
	TransactionProtocol
	BlockCodec
	BlockFactory
	BlockValidator
}
type ExecutionProtocol interface {
	ProtocolBound
	StateCommitter
}
type CoordinatorProtocol interface {
	CandidateValidator
	ProtocolBound
	BlockCodec
	BlockValidator
}
type GenesisProvider interface {
	Genesis(context.Context) (Block, map[string][]byte, error)
}
type LedgerProtocol interface {
	ProtocolBound
	TransactionCodec
	BlockCodec
	GenesisProvider
	BlockValidator
	StateCommitter
}

// Protocol owns format and semantics for a chain instance. Draft and Finalize
// never persist. ValidateBlock checks ancestry, commitments and binding, using
// ChainStore to resolve extra references when necessary. Implementers can
// compose these smaller interfaces rather than put every method on one object.
// Execute binds BlockID to the draft; Finalize returns the sealed block, and the
// builder then rebinds BlockID to its final ID before validation and persistence.
type Protocol interface {
	CandidateValidator
	ProtocolBound
	TransactionCodec
	TransactionValidator
	BlockCodec
	BlockFactory
	BlockValidator
	StateCommitter
}

var (
	ErrFull                = errors.New("capacity exceeded")
	ErrConflict            = errors.New("parent head conflict")
	ErrAlreadyCommitted    = errors.New("already committed")
	ErrClosed              = errors.New("component closed")
	ErrNotFound            = errors.New("not found")
	ErrInvalidTransaction  = errors.New("invalid transaction")
	ErrCommitUnknown       = errors.New("commit outcome unknown; query head")
	ErrLedgerMismatch      = errors.New("ledger instance mismatch")
	ErrNoProposal          = errors.New("no proposal currently available")
	ErrDeferredTransaction = errors.New("transaction temporarily not executable")
)

type TransactionDisposition uint8

const (
	TransactionRejected TransactionDisposition = iota + 1
	TransactionDeferred
)

// TransactionError identifies the exact candidate and its protocol-defined
// disposition. Unknown/internal executor failures must not use this type.
type TransactionError struct {
	TransactionID ID
	Disposition   TransactionDisposition
	Cause         error
}

func (e *TransactionError) Error() string {
	return fmt.Sprintf("transaction disposition %d: %v", e.Disposition, e.Cause)
}
func (e *TransactionError) Unwrap() error { return e.Cause }
func (e *TransactionError) Is(target error) bool {
	return target == ErrInvalidTransaction && e.Disposition == TransactionRejected || target == ErrDeferredTransaction && e.Disposition == TransactionDeferred
}

// CommittedError reports a completed durable commit followed by maintenance
// failure. Retrying the transaction blindly is incorrect; query BlockID instead.
type CommittedError struct {
	BlockID ID
	Cause   error
}

func (e *CommittedError) Error() string {
	return fmt.Sprintf("block committed; maintenance failed: %v", e.Cause)
}
func (e *CommittedError) Unwrap() error { return e.Cause }

type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Node owns the runtime. Run blocks, supervises workers and releases resources;
// normal cancellation is a graceful stop, not an unobserved background failure.
type Node interface{ Run(context.Context) error }

// Mempool owns encoded/decoded copies, is concurrency-safe and non-destructive
// on Candidates. Selection and byte budgeting belong to TransactionSelector.
type Mempool interface {
	ProtocolBound
	Add(context.Context, Transaction) error
	Candidates(context.Context) ([]Transaction, error)
	Remove(context.Context, []ID) error
}

type TransactionSelector interface {
	ProtocolBound
	Select(context.Context, []Transaction, int) ([]Transaction, error)
}

// StateReader is an immutable isolated snapshot. Reads return owned bytes/maps.
// Entries supports reference full-state hashing; large backends may add iterators.
type StateReader interface {
	Get(context.Context, []byte) ([]byte, error)
	Entries(context.Context) (map[string][]byte, error)
	Root() Commitment
	Close() error
}
type StateStore interface {
	ProtocolBound
	Snapshot(context.Context, ID) (StateReader, error)
}

// Executor returns isolated changes; invalid candidates never mutate state.
type Executor interface {
	ProtocolBound
	Execute(context.Context, StateReader, Block) (ExecutionResult, error)
}
type Committer interface {
	ProtocolBound
	Commit(context.Context, ID, Block, ExecutionResult) error
}

// Head is this backend's canonical cursor, not a prescribed header field.
// Multi-head protocols may add capabilities or use scoped store instances.
type ChainStore interface {
	ProtocolBound
	Head(context.Context) (ID, error)
	Block(context.Context, ID) (Block, error)
	Receipt(context.Context, ID) (Receipt, error)
}
type TransactionStore interface {
	ProtocolBound
	Transaction(context.Context, ID) (ConfirmedTransaction, error)
}

// LedgerIdentity is an in-process owner token, distinct from protocol/network
// identity. Every read view and committer for one backend share this token.
type LedgerIdentity struct{ marker byte }

func NewLedgerIdentity() *LedgerIdentity { return &LedgerIdentity{} }

type LedgerBound interface{ LedgerID() *LedgerIdentity }
type LedgerReader interface {
	ChainStore
	StateStore
	LedgerBound
}

// Ledger keeps the atomic transaction owner whole while consumers use read-only
// views. Protocol equality alone does not imply that two instances share data.
type Ledger interface {
	LedgerReader
	TransactionStore
	Committer
}
type BlockBuilder interface {
	ProtocolBound
	LedgerBound
	Build(context.Context, ID) (Block, ExecutionResult, error)
}
type ConsensusHost interface {
	Propose(context.Context) (Block, error)
	Validate(context.Context, Block) error
	Commit(context.Context, Block) error
}

// Consensus implementations must check extra semantic requirements explicitly.
type Consensus interface {
	Run(context.Context, ConsensusHost) error
}
type Message struct {
	Peer     string
	Protocol string
	Payload  []byte
}
type Transport interface {
	Run(context.Context, func(context.Context, Message) error) error
	Broadcast(context.Context, Message) error
}
