package model

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// Apply uses precisely the same mutation semantics as ledger persistence.
func Apply(ctx context.Context, parent map[string][]byte, change Transition) (map[string][]byte, error) {
	return core.ApplyStateChanges(ctx, parent, change.Writes, change.Deletes)
}
