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
