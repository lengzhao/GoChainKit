// Package model defines transaction/state semantics independently of consensus.
// Models need not have accounts, balances, nonces, spendable outputs or staking.
package model

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// Effect carries model-defined output for optional adapters. Generic execution
// never interprets its schema as a balance, fee, stake or consensus vote.
type Effect struct {
	Kind string
	Data []byte
}
type Transition struct {
	Writes   map[string][]byte
	Deletes  []string
	Receipts []core.Receipt
	Effects  []Effect
}

// Engine returns isolated transaction changes. Model identity/configuration must
// be included in the composed protocol's genesis binding by the assembler.
type Engine interface {
	core.ProtocolBound
	core.TransactionCodec
	core.TransactionValidator
	Genesis(context.Context) (map[string][]byte, error)
	Execute(context.Context, core.StateReader, []core.Transaction) (Transition, error)
}

// ExecutionContext exposes block/history context without prescribing height,
// balance, validator, fee or parent topology fields on every model. Parent is
// the execution baseline; additional ancestry remains protocol-defined.
// Applications may supply additional typed capabilities on the concrete value.
// BlockHistory excludes canonical transaction/receipt indices, which are not
// stable execution inputs when replaying a side branch. Implementations scope
// blocks to the execution baseline's deterministic ancestry.
type BlockHistory interface {
	core.ProtocolBound
	Head(context.Context) (core.ID, error)
	Block(context.Context, core.ID) (core.Block, error)
}

type ExecutionContext interface {
	Parent() core.Block
	Candidate() core.Block
	History() BlockHistory
}

// ContextualEngine is optional for time locks, maturity or historical rules.
// Generic assemblers preserve Engine compatibility and use this when available.
type ContextualEngine interface {
	Engine
	ExecuteInContext(context.Context, core.StateReader, []core.Transaction, ExecutionContext) (Transition, error)
}

// HeightContext optionally exposes the execution candidate's protocol height.
// Height is not required by every model or protocol family.
type HeightContext interface{ Height() uint64 }

// TimestampContext optionally exposes the candidate's agreed timestamp, not a
// fresh local clock reading. Units and validity belong to the protocol family.
type TimestampContext interface{ Timestamp() int64 }

// StateCommitmentContext computes commitments using the active protocol's
// deterministic state encoding. It must not mutate the supplied state.
type StateCommitmentContext interface {
	StateCommitment(context.Context, map[string][]byte) (core.Commitment, error)
}
