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

// Provider derives a deterministic ordered member set from an already validated
// parent supplied by its consumer. Identity commits membership policy/config;
// neither mutable local heads nor ledger dependencies belong in this contract.
// Static providers anchor the snapshot without claiming historical existence.
// Returned slices and public key bytes belong to the caller.
type Provider interface {
	Identity() core.ID
	Members(context.Context, core.Block) (Set, error)
}
