// Package node coordinates chain capabilities without importing concrete plugins.
package node

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"math"
	"sync"
)

type CoordinatorDeps struct {
	Builder   contracts.BlockBuilder `json:"builder"`
	Chain     contracts.ChainStore   `json:"chain"`
	States    contracts.StateStore   `json:"states"`
	Executor  contracts.Executor     `json:"executor"`
	Committer contracts.Committer    `json:"committer"`
	Pool      contracts.Mempool      `json:"pool"`
}

// Coordinator serializes validation and finalization. Storage remains responsible
// for an atomic parent-head check against writers outside this coordinator.
type Coordinator struct {
	mu   sync.Mutex
	deps CoordinatorDeps
}

func NewCoordinator(d CoordinatorDeps) (*Coordinator, error) {
	if d.Builder == nil || d.Chain == nil || d.States == nil || d.Executor == nil || d.Committer == nil || d.Pool == nil {
		return nil, fmt.Errorf("node: all coordinator dependencies required")
	}
	return &Coordinator{deps: d}, nil
}
func (c *Coordinator) Propose(ctx context.Context) (types.Block, error) {
	head, err := c.deps.Chain.Head(ctx)
	if err != nil {
		return types.Block{}, err
	}
	b, _, err := c.deps.Builder.Build(ctx, head)
	return b, err
}
func (c *Coordinator) execute(ctx context.Context, b types.Block) (types.ExecutionResult, error) {
	fail := func(err error) (types.ExecutionResult, error) { return types.ExecutionResult{}, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	head, err := c.deps.Chain.Head(ctx)
	if err != nil {
		return fail(err)
	}
	if b.Header.Parent != head {
		return fail(contracts.ErrConflict)
	}
	parent, err := c.deps.Chain.Block(ctx, head)
	if err != nil {
		return fail(err)
	}
	if parent.Header.Height == math.MaxUint64 || b.Header.Height != parent.Header.Height+1 || b.Header.ChainID != parent.Header.ChainID {
		return fail(fmt.Errorf("invalid block ancestry"))
	}
	id, err := b.ID()
	if err != nil {
		return fail(err)
	}
	state, err := c.deps.States.Snapshot(ctx, head)
	if err != nil {
		return fail(err)
	}
	result, runErr := c.deps.Executor.Execute(ctx, state, b.Clone())
	closeErr := state.Close()
	if runErr != nil {
		return fail(runErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if result.Parent != head || result.StateRoot != b.Header.StateRoot || types.ReceiptRoot(result.Receipts) != b.Header.ReceiptRoot {
		return fail(fmt.Errorf("execution commitment mismatch"))
	}
	result.BlockID = id
	return result, nil
}
func (c *Coordinator) Validate(ctx context.Context, b types.Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.execute(ctx, b.Clone())
	return err
}

// Commit revalidates against the current parent rather than trusting cached
// proposal results. Cleanup failure is reported explicitly as post-commit.
func (c *Coordinator) Commit(ctx context.Context, b types.Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b = b.Clone()
	result, err := c.execute(ctx, b)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.deps.Committer.Commit(ctx, b.Header.Parent, b, result); err != nil {
		return err
	}
	ids := make([]types.Hash, len(b.Transactions))
	for i, tx := range b.Transactions {
		id, err := tx.ID()
		if err != nil {
			return fmt.Errorf("block committed; transaction ID: %w", err)
		}
		ids[i] = id
	}
	if err := c.deps.Pool.Remove(ctx, ids); err != nil {
		return fmt.Errorf("block committed; pool cleanup: %w", err)
	}
	return nil
}
