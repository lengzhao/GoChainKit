package core

import "context"

// BranchProtocol is the optional single-parent branch capability. General
// Protocols and Blocks remain free to describe other ancestry topologies.
type BranchProtocol interface {
	LedgerProtocol
	Parent(Block) (ID, error)
}

// BranchPolicy supplies deterministic chain ranking and finality. Scores are
// opaque caller-owned bytes; storage never interprets them. Genesis has a nil
// parent score. PolicyID must bind rule version and all semantic configuration.
// Callbacks receive read-only committed history, outside storage transactions.
// Finalized must return an ancestor of b (or empty for no finality). The store
// independently enforces monotonic finality across canonical switches.
type BranchPolicy interface {
	ProtocolBound
	PolicyID() string
	Score(context.Context, ChainStore, Block, []byte) ([]byte, error)
	Prefer(context.Context, ChainStore, Block, Block, []byte, []byte) (bool, error)
	Finalized(context.Context, ChainStore, Block) (ID, error)
}
