// Package asset defines optional fixed-address currency and escrow capabilities.
// These contracts do not constrain core objects or general execution models.
package asset

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
)

type Address [32]byte

type Allocation struct {
	Address Address
	Amount  uint64
}
type Mint struct {
	ID     core.ID
	To     Address
	Amount uint64
}
type Currency interface {
	model.Engine
	Credit(context.Context, core.StateReader, Mint) (model.Transition, error)
}

// HeightCurrency optionally binds issuance to candidate height (e.g. coinbase
// maturity). The executor supplies the same deterministic height on replay.
type HeightCurrency interface {
	Currency
	CreditAtHeight(context.Context, core.StateReader, Mint, uint64) (model.Transition, error)
}

const FeeEffect = "asset/fees/v1"

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
