package core

import (
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
