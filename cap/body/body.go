// Package body defines optional hydration for hash-only block bodies. These
// contracts do not yet imply support in the default ledger or network pipeline.
package body

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// References is implemented by a protocol adapter, not a network client.
// References returns ordered transaction IDs including any permitted repeats.
// Attach rejects missing, reordered or mismatched IDs and verifies the protocol's
// body commitment. The hydrated object retains the same Block.ID and canonical
// wire encoding as the reference-only object; transactions are auxiliary data.
type References interface {
	core.ProtocolBound
	References(core.Block) ([]core.ID, error)
	Attach(core.Block, []core.Transaction) (core.Block, error)
}

// TransactionSource supplies validated identities from storage or a bounded
// network adapter. A result must match the requested ID; callers recheck it.
type TransactionSource interface {
	core.ProtocolBound
	Transaction(context.Context, core.ID) (core.Transaction, error)
}

// Materializer composes References with sources. It returns only after complete
// ordered hydration and commitment validation, or an error; never partial data.
// Implementations enforce request size, total bytes and concurrency budgets and
// reject incompatible protocol/genesis bindings before fetching remote data.
type Materializer interface {
	core.ProtocolBound
	Materialize(context.Context, core.Block) (core.Block, error)
}
