package consensus

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"math/big"
)

// WorkProtocol adds parent linkage and independently verifiable proof-of-work.
// Work returns an owned positive integer for valid non-genesis blocks. Genesis
// may have zero work. Seal returns an immutable mined block and honors cancellation.
type WorkProtocol interface {
	core.Protocol
	Parent(core.Block) (core.ID, error)
	Work(core.Block) (*big.Int, error)
	BlockSealer
}

// ProposalValidator checks an execution-bound finalized proposal before consensus
// sealing. Storage always calls ValidateBlock and must reject unsealed proposals.
// Protocols without this optional capability retain finalized-block validation.
type ProposalValidator interface {
	ValidateProposal(context.Context, core.ChainStore, core.Block, core.Block, core.ExecutionResult) error
}

// BlockSealer performs consensus proof production after execution finalization.
type BlockSealer interface {
	Seal(context.Context, core.Block) (core.Block, error)
}
