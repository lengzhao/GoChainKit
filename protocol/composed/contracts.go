// Package composed implements one optional envelope family assembled from a
// transaction/state engine and independent consensus and chain-choice rules.
package composed

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"math/big"
)

type Header struct {
	Version                                   uint32
	Binding                                   []byte
	Parent                                    []byte
	Height                                    uint64
	Timestamp                                 int64
	TransactionsRoot, StateRoot, ReceiptsRoot []byte
	Producer                                  []byte
	Consensus                                 []byte
}
type Envelope struct {
	Header       Header
	Transactions [][]byte
	Proof        []byte
}

// Rules bind their complete semantic configuration into ID. Draft does not
// produce a consensus proof. Draft must be deterministic from its parent and
// next header: no local signer, producer substitution or fresh clock reads may
// change its output, because execution views reconstruct it during replay.
// Validate(false) checks a proposal; Validate(true)
// additionally checks its proof. History only contains already stored ancestors.
// The builder validates a proposal after Finalize binds execution commitments.
// A sealing runtime must validate the sealed block again before import; storage
// performs full validation and must not accept an unsealed proposal. Validation
// is read-only. Future state-aware capabilities must bind inputs to the execution
// parent and apply identically to production and replay.
type Rule interface {
	ID() string
	Genesis(Header) ([]byte, error)
	Draft(context.Context, Envelope, Header) ([]byte, error)
	Validate(context.Context, core.ChainStore, Envelope, Envelope, bool) error
}

// Choice owns score encoding and comparison. Generic callers must not interpret
// or lexicographically sort score bytes; Prefer is the comparison entry point.
// A work-based implementation may derive its own score from WorkRule.Work.
type Choice interface {
	ID() string
	Score(context.Context, core.ChainStore, Envelope, []byte) ([]byte, error)
	Prefer(context.Context, core.ChainStore, Envelope, Envelope, []byte, []byte) (bool, error)
	Finalized(context.Context, core.ChainStore, Envelope) (core.ID, error)
}
type SealingRule interface {
	Rule
	Seal(context.Context, Envelope) (Envelope, error)
}
type WorkRule interface {
	SealingRule
	Work(Envelope) (*big.Int, error)
}

// Economics is optional and entirely separate from generic state execution.
// A Rule supplies reward amounts as consensus semantics; Protocol applies them
// through the model's optional asset.Currency capability. Fee burn/distribution
// policies are separate extension concerns, not implicit additions to Reward.
// Enabled distinguishes a no-reward/no-fee chain from a currency-based chain.
// Capability activation is resolved once during protocol construction.
type Economics interface {
	Enabled() bool
	Reward(uint64) (uint64, error)
}
