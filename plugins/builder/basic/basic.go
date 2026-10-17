// Package basic builds isolated proposals using injected chain capabilities.
package basic

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"github.com/lengzhao/pluginkit"
	"math"
)

type Config struct {
	ChainID             string `json:"chainID"`
	MaxTransactionBytes int    `json:"maxTransactionBytes"`
}
type Deps struct {
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
func New(cfg Config, deps Deps) (contracts.BlockBuilder, error) {
	if cfg.ChainID == "" || len(cfg.ChainID) > types.MaxChainIDBytes || cfg.MaxTransactionBytes <= 0 {
		return nil, fmt.Errorf("builder/basic: chainID and positive byte budget required")
	}
	if deps.Pool == nil || deps.Chain == nil || deps.States == nil || deps.Executor == nil {
		return nil, fmt.Errorf("builder/basic: pool, chain, states and executor required")
	}
	return &builder{cfg: cfg, deps: deps}, nil
}
func (b *builder) Build(ctx context.Context, head types.Hash) (types.Block, types.ExecutionResult, error) {
	fail := func(err error) (types.Block, types.ExecutionResult, error) {
		return types.Block{}, types.ExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	parent, err := b.deps.Chain.Block(ctx, head)
	if err != nil {
		return fail(fmt.Errorf("parent: %w", err))
	}
	parentID, err := parent.ID()
	if err != nil {
		return fail(err)
	}
	if parentID != head || parent.Header.ChainID != b.cfg.ChainID || parent.Header.Height == math.MaxUint64 {
		return fail(fmt.Errorf("invalid parent"))
	}
	txs, err := b.deps.Pool.Select(ctx, b.cfg.MaxTransactionBytes)
	if err != nil {
		return fail(err)
	}
	budget := b.cfg.MaxTransactionBytes
	for i, tx := range txs {
		if err := tx.Verify(b.cfg.ChainID); err != nil {
			return fail(fmt.Errorf("%w: %v", contracts.ErrInvalidTransaction, err))
		}
		encoded, err := tx.MarshalBinary()
		if err != nil {
			return fail(err)
		}
		if len(encoded) > budget {
			return fail(contracts.ErrFull)
		}
		budget -= len(encoded)
		txs[i] = tx.Clone()
	}
	root, err := types.TransactionRoot(txs)
	if err != nil {
		return fail(err)
	}
	block := types.Block{Header: types.BlockHeader{Version: 1, ChainID: b.cfg.ChainID, Height: parent.Header.Height + 1, Parent: head, TransactionRoot: root}, Transactions: txs}
	state, err := b.deps.States.Snapshot(ctx, head)
	if err != nil {
		return fail(err)
	}
	result, executeErr := b.deps.Executor.Execute(ctx, state, block.Clone())
	closeErr := state.Close()
	if executeErr != nil {
		return fail(fmt.Errorf("execute: %w", executeErr))
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if result.Parent != head {
		return fail(fmt.Errorf("execution parent mismatch"))
	}
	block.Header.StateRoot = result.StateRoot
	block.Header.ReceiptRoot = types.ReceiptRoot(result.Receipts)
	id, err := block.ID()
	if err != nil {
		return fail(err)
	}
	result.BlockID = id
	return block, result, nil
}
