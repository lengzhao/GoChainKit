// Package query defines small optional API-facing capabilities.
package query

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// Submitter performs transaction admission; successful admission does not mean
// confirmation. Implementation applies protocol validation and bounded limits.
type Submitter interface {
	core.ProtocolBound
	Submit(context.Context, core.Transaction) error
}

// HeightIndex is optional for height-based chain families. Anchor fixes the
// queried history (including a historical or side-branch head), so concurrent
// canonical-head changes cannot change the result. Missing blocks/anchors use
// core.ErrNotFound; families without heights need not implement this interface.
type HeightIndex interface {
	core.ProtocolBound
	AtHeight(context.Context, core.ID, uint64) (core.Block, error)
}
