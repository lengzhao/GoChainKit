package asset

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
)

// Escrow is an optional asset adapter for custody/locking rules such as staking.
// Callers must authenticate ownership and authorize release before invoking it;
// neither operation is a public transaction or a source of voting authority.
type Escrow interface {
	Currency
	Lock(context.Context, core.StateReader, Lock) (model.Transition, error)
	Release(context.Context, core.StateReader, Release) (model.Transition, error)
}
type Lock struct {
	ID     core.ID
	Owner  Address
	Amount uint64
}
type Release struct {
	ID core.ID
	To Address
}
