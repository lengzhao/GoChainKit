package core

import (
	"context"
	"math/big"
)

// WorkProtocol adds parent linkage and independently verifiable proof-of-work.
// Work returns an owned positive integer for valid non-genesis blocks. Genesis
// may have zero work. Seal returns an immutable mined block and honors cancellation.
type WorkProtocol interface {
	Protocol
	Parent(Block) (ID, error)
	Work(Block) (*big.Int, error)
	BlockSealer
}

// BranchChange describes a canonical switch. Detached is old tip first; Attached
// is ancestor first. Neither includes the common ancestor. Equal work keeps head.
type BranchChange struct {
	OldHead, NewHead   ID
	Detached, Attached []Block
}
type BranchLedger interface {
	Ledger
	Import(context.Context, ID, Block, ExecutionResult) (BranchChange, error)
}
