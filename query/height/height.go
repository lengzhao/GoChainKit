// Package height resolves a height against a fixed ancestry, never a moving head.
package height

import (
	"context"
	"fmt"
	querycap "github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
)

type Protocol interface {
	core.ProtocolBound
	Parent(core.Block) (core.ID, error)
	BlockHeight(core.Block) (uint64, error)
}
type Ledger interface {
	core.ChainStore
	core.LedgerBound
}
type Config struct {
	MaxDepth int `json:"maxDepth"`
}
type Deps struct {
	Protocol Protocol `json:"protocol"`
	Ledger   Ledger   `json:"ledger"`
}
type Index struct {
	d   Deps
	max int
}

func New(c Config, d Deps) (*Index, error) {
	if d.Protocol == nil || d.Ledger == nil || d.Ledger.LedgerID() == nil {
		return nil, fmt.Errorf("height: protocol and ledger required")
	}
	if e := core.RequireCompatible(d.Protocol.Protocol(), d.Ledger); e != nil {
		return nil, e
	}
	if c.MaxDepth == 0 {
		c.MaxDepth = 100000
	}
	if c.MaxDepth < 1 {
		return nil, fmt.Errorf("height: positive depth required")
	}
	return &Index{d, c.MaxDepth}, nil
}
func (i *Index) Protocol() core.ProtocolDescriptor { return i.d.Protocol.Protocol() }
func (i *Index) LedgerID() *core.LedgerIdentity    { return i.d.Ledger.LedgerID() }
func (i *Index) AtHeight(ctx context.Context, anchor core.ID, height uint64) (core.Block, error) {
	if anchor == "" {
		return nil, core.ErrNotFound
	}
	seen := map[core.ID]bool{}
	var previous uint64
	for depth := 0; depth < i.max; depth++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if seen[anchor] {
			return nil, fmt.Errorf("height: ancestry cycle")
		}
		seen[anchor] = true
		b, e := i.d.Ledger.Block(ctx, anchor)
		if e != nil {
			return nil, e
		}
		if b == nil || b.ID() != anchor {
			return nil, fmt.Errorf("height: identity mismatch")
		}
		h, e := i.d.Protocol.BlockHeight(b)
		if e != nil {
			return nil, e
		}
		if depth > 0 && (h >= previous || previous-h != 1) {
			return nil, fmt.Errorf("height: discontinuous ancestry")
		}
		if h == height {
			return b, nil
		}
		if h < height {
			return nil, core.ErrNotFound
		}
		previous = h
		anchor, e = i.d.Protocol.Parent(b)
		if e != nil {
			return nil, e
		}
		if anchor == "" {
			return nil, core.ErrNotFound
		}
	}
	return nil, core.ErrFull
}

var _ querycap.HeightIndex = (*Index)(nil)
