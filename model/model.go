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
type ExecutionContext interface {
	Parent() core.Block
	Candidate() core.Block
	History() core.ChainStore
}

// ContextualEngine is optional for time locks, maturity or historical rules.
// Generic assemblers preserve Engine compatibility and use this when available.
type ContextualEngine interface {
	Engine
	ExecuteInContext(context.Context, core.StateReader, []core.Transaction, ExecutionContext) (Transition, error)
}
