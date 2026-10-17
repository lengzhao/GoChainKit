// Package builder assembles candidates through protocol and selection policies.
package builder

import (
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
)

type BasicConfig struct {
	MaxTransactionBytes int `json:"maxTransactionBytes"`
}
type Deps struct {
	Protocol core.BuilderProtocol     `json:"protocol"`
	Pool     core.Mempool             `json:"pool"`
	Ledger   core.LedgerReader        `json:"ledger"`
	Executor core.Executor            `json:"executor"`
	Selector core.TransactionSelector `json:"selector"`
}
type builder struct {
	cfg  BasicConfig
	deps Deps
}

func init() { pluginkit.Register("builder/basic", NewBasic) }
func NewBasic(cfg BasicConfig, d Deps) (core.BlockBuilder, error) {
	if cfg.MaxTransactionBytes <= 0 || d.Protocol == nil || d.Pool == nil || d.Ledger == nil || d.Executor == nil || d.Selector == nil {
		return nil, fmt.Errorf("builder/basic: dependencies and positive budget required")
	}
	if d.Ledger.LedgerID() == nil {
		return nil, core.ErrLedgerMismatch
	}
	if err := core.RequireCompatible(d.Protocol.Protocol(), d.Pool, d.Ledger, d.Executor, d.Selector); err != nil {
		return nil, err
	}
	return &builder{cfg, d}, nil
}
func (b *builder) Protocol() core.ProtocolDescriptor { return b.deps.Protocol.Protocol() }
func (b *builder) LedgerID() *core.LedgerIdentity    { return b.deps.Ledger.LedgerID() }
func (b *builder) Build(ctx context.Context, head core.ID) (core.Block, core.ExecutionResult, error) {
	fail := func(err error) (core.Block, core.ExecutionResult, error) { return nil, core.ExecutionResult{}, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	parent, err := b.deps.Ledger.Block(ctx, head)
	if err != nil {
		return fail(err)
	}
	if parent == nil || parent.ID() != head {
		return fail(fmt.Errorf("invalid parent identity"))
	}
	candidates, err := b.deps.Pool.Candidates(ctx)
	if err != nil {
		return fail(err)
	}
	selected, err := b.deps.Selector.Select(ctx, candidates, b.cfg.MaxTransactionBytes)
	if err != nil {
		return fail(err)
	}
	txs := make([]core.Transaction, len(selected))
	budget := b.cfg.MaxTransactionBytes
	for i, t := range selected {
		if t == nil || t.ID() == "" {
			return fail(core.ErrInvalidTransaction)
		}
		if err := b.deps.Protocol.ValidateTransaction(ctx, t); err != nil {
			return fail(fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err))
		}
		data, err := b.deps.Protocol.EncodeTransaction(t)
		if err != nil {
			return fail(err)
		}
		if len(data) > budget {
			return fail(core.ErrFull)
		}
		budget -= len(data)
		txs[i], err = b.deps.Protocol.DecodeTransaction(data)
		if err != nil {
			return fail(err)
		}
		if txs[i] == nil || txs[i].ID() != t.ID() {
			return fail(fmt.Errorf("codec changed transaction identity"))
		}
	}
	// Each retry removes exactly one candidate from this proposal. Deferred items
	// remain in the pool; rejected items are evicted. Work is bounded by candidates+1.
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		draft, err := b.deps.Protocol.Draft(ctx, parent, txs)
		if err != nil {
			return fail(err)
		}
		snapshot, err := b.deps.Ledger.Snapshot(ctx, head)
		if err != nil {
			return fail(err)
		}
		result, runErr := b.deps.Executor.Execute(ctx, snapshot, draft)
		closeErr := snapshot.Close()
		if closeErr != nil {
			return fail(fmt.Errorf("close execution snapshot: %w", closeErr))
		}
		if runErr != nil {
			var disposition *core.TransactionError
			if !errors.As(runErr, &disposition) || disposition == nil || (disposition.Disposition != core.TransactionRejected && disposition.Disposition != core.TransactionDeferred) {
				return fail(runErr)
			}
			index := -1
			for i, t := range txs {
				if t.ID() == disposition.TransactionID {
					index = i
					break
				}
			}
			if index < 0 {
				return fail(fmt.Errorf("executor disposition references absent candidate: %w", runErr))
			}
			if disposition.Disposition == core.TransactionRejected {
				if err := b.deps.Pool.Remove(ctx, []core.ID{disposition.TransactionID}); err != nil {
					return fail(err)
				}
			}
			txs = append(txs[:index], txs[index+1:]...)
			continue
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
		validate := b.deps.Protocol.ValidateBlock
		if proposals, ok := b.deps.Protocol.(core.ProposalValidator); ok {
			validate = proposals.ValidateProposal
		}
		if err := validate(ctx, b.deps.Ledger, parent, block, result); err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		return block, result, nil
	}
}
