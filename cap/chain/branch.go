package chain

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// BranchProtocol is the optional single-parent branch capability. General
// Protocols and Blocks remain free to describe other ancestry topologies.
type BranchProtocol interface {
	core.LedgerProtocol
	Parent(core.Block) (core.ID, error)
}

// BranchPolicy supplies deterministic chain ranking and finality. Scores are
// opaque caller-owned bytes; storage never interprets them. Genesis has a nil
// parent score. PolicyID must bind rule version and all semantic configuration.
// Callbacks receive read-only committed history, outside storage transactions.
// Finalized must return an ancestor of b (or empty for no finality). The store
// independently enforces monotonic finality across canonical switches.
type BranchPolicy interface {
	core.ProtocolBound
	PolicyID() string
	Score(context.Context, core.ChainStore, core.Block, []byte) ([]byte, error)
	Prefer(context.Context, core.ChainStore, core.Block, core.Block, []byte, []byte) (bool, error)
	Finalized(context.Context, core.ChainStore, core.Block) (core.ID, error)
}

// BranchChange describes a canonical switch. Detached is old tip first; Attached
// is ancestor first. Neither includes the common ancestor. Selection and tie behavior belong to BranchPolicy.
type BranchChange struct {
	OldHead, NewHead   core.ID
	Detached, Attached []core.Block
}
type BranchLedger interface {
	core.Ledger
	Import(context.Context, core.ID, core.Block, core.ExecutionResult) (BranchChange, error)
}
