// Package node orchestrates protocol-bound capabilities without concrete formats.
package node

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
)

type CoordinatorDeps struct {
	Protocol  contracts.Protocol     `json:"protocol"`
	Builder   contracts.BlockBuilder `json:"builder"`
	Chain     contracts.ChainStore   `json:"chain"`
	States    contracts.StateStore   `json:"states"`
	Executor  contracts.Executor     `json:"executor"`
	Committer contracts.Committer    `json:"committer"`
	Pool      contracts.Mempool      `json:"pool"`
}

// Coordinator serializes host operations with a cancellation-aware gate. Backends
// still recheck the parent atomically against writers outside this coordinator.
type Coordinator struct {
	gate chan struct{}
	deps CoordinatorDeps
}

func NewCoordinator(d CoordinatorDeps) (*Coordinator, error) {
	if d.Protocol == nil || d.Builder == nil || d.Chain == nil || d.States == nil || d.Executor == nil || d.Committer == nil || d.Pool == nil {
		return nil, fmt.Errorf("node: all protocol-bound dependencies required")
	}
	if err := contracts.RequireCompatible(d.Protocol.Protocol(), d.Builder, d.Chain, d.States, d.Executor, d.Committer, d.Pool); err != nil {
		return nil, err
	}
	return &Coordinator{gate: make(chan struct{}, 1), deps: d}, nil
}
func (c *Coordinator) acquire(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-c.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Coordinator) release() { <-c.gate }
func (c *Coordinator) freeze(b contracts.Block) (contracts.Block, error) {
	if b == nil || b.ID() == "" {
		return nil, fmt.Errorf("empty block")
	}
	data, err := c.deps.Protocol.EncodeBlock(b)
	if err != nil {
		return nil, err
	}
	copy, err := c.deps.Protocol.DecodeBlock(data)
	if err != nil {
		return nil, err
	}
	if copy == nil || copy.ID() != b.ID() {
		return nil, fmt.Errorf("block codec changed identity")
	}
	return copy, nil
}
func (c *Coordinator) Propose(ctx context.Context) (contracts.Block, error) {
	head, err := c.deps.Chain.Head(ctx)
	if err != nil {
		return nil, err
	}
	block, _, err := c.deps.Builder.Build(ctx, head)
	if err != nil {
		return nil, err
	}
	return c.freeze(block)
}
func (c *Coordinator) execute(ctx context.Context, b contracts.Block) (contracts.ExecutionResult, error) {
	fail := func(err error) (contracts.ExecutionResult, error) { return contracts.ExecutionResult{}, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	head, err := c.deps.Chain.Head(ctx)
	if err != nil {
		return fail(err)
	}
	parent, err := c.deps.Chain.Block(ctx, head)
	if err != nil {
		return fail(err)
	}
	state, err := c.deps.States.Snapshot(ctx, head)
	if err != nil {
		return fail(err)
	}
	result, runErr := c.deps.Executor.Execute(ctx, state, b)
	closeErr := state.Close()
	if runErr != nil {
		return fail(runErr)
	}
	if closeErr != nil {
		return fail(closeErr)
	}
	if result.Parent != head {
		return fail(contracts.ErrConflict)
	}
	result.BlockID = b.ID()
	if err := c.deps.Protocol.ValidateBlock(ctx, c.deps.Chain, parent, b, result); err != nil {
		return fail(err)
	}
	return result, nil
}
func (c *Coordinator) Validate(ctx context.Context, b contracts.Block) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	copy, err := c.freeze(b)
	if err != nil {
		return err
	}
	_, err = c.execute(ctx, copy)
	return err
}
func (c *Coordinator) Commit(ctx context.Context, b contracts.Block) error {
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	copy, err := c.freeze(b)
	if err != nil {
		return err
	}
	result, err := c.execute(ctx, copy)
	if err != nil {
		return err
	}
	txs, err := c.deps.Protocol.Transactions(copy)
	if err != nil {
		return err
	}
	ids := make([]contracts.ID, len(txs))
	for i, t := range txs {
		if t == nil || t.ID() == "" {
			return fmt.Errorf("empty transaction identity")
		}
		ids[i] = t.ID()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.deps.Committer.Commit(ctx, result.Parent, copy, result); err != nil {
		return err
	}
	if err := c.deps.Pool.Remove(ctx, ids); err != nil {
		return fmt.Errorf("block committed; pool cleanup: %w", err)
	}
	return nil
}
