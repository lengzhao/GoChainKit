// Package validator defines historical membership for consensus consumers.
package validator

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/signer"
	"github.com/lengzhao/GoChainKit/core"
)

type Member struct {
	ID  core.ID
	Key signer.PublicKey
}

// Set is an ordered, immutable snapshot. ID commits to the ordered members and
// relevant membership rules; Anchor is the requested execution parent. Members
// have unique nonempty identities. Stake and voting weights are policy-specific.
type Set struct {
	Anchor  core.ID
	ID      core.ID
	Members []Member
}

// ValidatorSetProvider derives membership from the specified parent's agreed
// state, never from a mutable current-head or local operator list. Returned
// slices and keys are caller-owned. Unknown anchors return core.ErrNotFound.
type ValidatorSetProvider interface {
	core.ProtocolBound
	Validators(context.Context, core.ID) (Set, error)
}
