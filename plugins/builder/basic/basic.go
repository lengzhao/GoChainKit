// Package basic assembles candidates without prescribing a block format.
package basic

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/pluginkit"
)

type Config struct {
	MaxTransactionBytes int `json:"maxTransactionBytes"`
}
type Deps struct {
	Protocol contracts.Protocol   `json:"protocol"`
	Pool     contracts.Mempool    `json:"pool"`
	Chain    contracts.ChainStore `json:"chain"`
	States   contracts.StateStore `json:"states"`
	Executor contracts.Executor   `json:"executor"`
}
type builder struct {
	cfg  Config
	deps Deps
}

func init() { pluginkit.Register("builder/basic", New) }
func New(cfg Config, d Deps) (contracts.BlockBuilder, error) {
	if cfg.MaxTransactionBytes <= 0 || d.Protocol == nil || d.Pool == nil || d.Chain == nil || d.States == nil || d.Executor == nil {
		return nil, fmt.Errorf("builder/basic: protocol, dependencies and positive budget required")
	}
	if err := contracts.RequireCompatible(d.Protocol.Protocol(), d.Pool, d.Chain, d.States, d.Executor); err != nil {
		return nil, err
	}
	return &builder{cfg, d}, nil
}
func (b *builder) Protocol() contracts.ProtocolDescriptor { return b.deps.Protocol.Protocol() }
func (b *builder) Build(ctx context.Context, head contracts.ID) (contracts.Block, contracts.ExecutionResult, error) {
	fail := func(err error) (contracts.Block, contracts.ExecutionResult, error) {
		return nil, contracts.ExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	parent, err := b.deps.Chain.Block(ctx, head)
	if err != nil {
		return fail(err)
	}
	if parent == nil || parent.ID() != head {
		return fail(fmt.Errorf("invalid parent identity"))
	}
	txs, err := b.deps.Pool.Select(ctx, b.cfg.MaxTransactionBytes)
	if err != nil {
		return fail(err)
	}
	budget := b.cfg.MaxTransactionBytes
	for i, t := range txs {
		if err := b.deps.Protocol.ValidateTransaction(ctx, t); err != nil {
			return fail(fmt.Errorf("%w: %v", contracts.ErrInvalidTransaction, err))
		}
		data, err := b.deps.Protocol.EncodeTransaction(t)
		if err != nil {
			return fail(err)
		}
		if len(data) > budget {
			return fail(contracts.ErrFull)
		}
		budget -= len(data)
		txs[i], err = b.deps.Protocol.DecodeTransaction(data)
		if err != nil {
			return fail(err)
		}
	}
	draft, err := b.deps.Protocol.Draft(ctx, parent, txs)
	if err != nil {
		return fail(err)
	}
	snapshot, err := b.deps.States.Snapshot(ctx, head)
	if err != nil {
		return fail(err)
	}
	result, runErr := b.deps.Executor.Execute(ctx, snapshot, draft)
	closeErr := snapshot.Close()
	if runErr != nil {
		return fail(runErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if result.Parent != head {
		return fail(fmt.Errorf("execution parent mismatch"))
	}
	block, err := b.deps.Protocol.Finalize(ctx, draft, result)
	if err != nil {
		return fail(err)
	}
	if block == nil || block.ID() == "" {
		return fail(fmt.Errorf("empty block identity"))
	}
	result.BlockID = block.ID()
	if err := b.deps.Protocol.ValidateBlock(ctx, b.deps.Chain, parent, block, result); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return block, result, nil
}
