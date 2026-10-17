package storage

import (
	"context"
	"fmt"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	"github.com/lengzhao/GoChainKit/core"
	"math/big"
)

// workPolicy preserves the legacy cumulative-work rule behind a policy adapter.
type workPolicy struct{ p consensuscap.WorkProtocol }

func (p workPolicy) Protocol() core.ProtocolDescriptor { return p.p.Protocol() }
func (p workPolicy) PolicyID() string                  { return "cumulative-work/v1" }
func (p workPolicy) Score(ctx context.Context, _ core.ChainStore, b core.Block, parent []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	w, e := p.p.Work(b)
	if e != nil {
		return nil, e
	}
	link, e := p.p.Parent(b)
	if e != nil {
		return nil, e
	}
	if w == nil || w.Sign() < 0 || link != "" && w.Sign() == 0 || link == "" && w.Sign() != 0 {
		return nil, fmt.Errorf("invalid block work")
	}
	return new(big.Int).Add(new(big.Int).SetBytes(parent), w).Bytes(), nil
}
func (p workPolicy) Prefer(ctx context.Context, _ core.ChainStore, _, _ core.Block, a, b []byte) (bool, error) {
	return new(big.Int).SetBytes(b).Cmp(new(big.Int).SetBytes(a)) > 0, ctx.Err()
}
func (p workPolicy) Finalized(ctx context.Context, _ core.ChainStore, _ core.Block) (core.ID, error) {
	return "", ctx.Err()
}
func resolvePolicy(d Deps) (chaincap.BranchPolicy, error) {
	policy := d.Policy
	if policy == nil {
		if p, ok := d.Protocol.(chaincap.BranchPolicy); ok {
			policy = p
		} else if p, ok := d.Protocol.(consensuscap.WorkProtocol); ok {
			policy = workPolicy{p}
		}
	}
	if policy == nil {
		return nil, nil
	}
	if _, ok := d.Protocol.(chaincap.BranchProtocol); !ok {
		return nil, fmt.Errorf("branch policy requires single-parent branch capability")
	}
	if policy.PolicyID() == "" {
		return nil, fmt.Errorf("empty branch policy identity")
	}
	if e := core.RequireCompatible(d.Protocol.Protocol(), policy); e != nil {
		return nil, e
	}
	return policy, nil
}

// readOnlyHistory intentionally hides concrete ledger mutation and lifecycle APIs.
type readOnlyHistory struct{ core.ChainStore }
type genesisHistory struct {
	p chaincap.BranchProtocol
	b core.Block
}

func (v genesisHistory) Protocol() core.ProtocolDescriptor         { return v.p.Protocol() }
func (v genesisHistory) Head(ctx context.Context) (core.ID, error) { return v.b.ID(), ctx.Err() }
func (v genesisHistory) Block(ctx context.Context, id core.ID) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if id != v.b.ID() {
		return nil, core.ErrNotFound
	}
	data, e := v.p.EncodeBlock(v.b)
	if e != nil {
		return nil, e
	}
	return v.p.DecodeBlock(data)
}
func (v genesisHistory) Receipt(ctx context.Context, _ core.ID) (core.Receipt, error) {
	if e := ctx.Err(); e != nil {
		return core.Receipt{}, e
	}
	return core.Receipt{}, core.ErrNotFound
}
