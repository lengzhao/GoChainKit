package replication

import (
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"sync"
	"time"
)

// ImportProtocol needs branch validation but never requires proof-of-work.
type ImportProtocol interface {
	core.ProtocolBound
	core.CandidateValidator
	core.BlockValidator
	Parent(core.Block) (core.ID, error)
	Transactions(core.Block) ([]core.Transaction, error)
}
type ImportDeps struct {
	Rules    ImportProtocol    `json:"protocol"`
	Ledger   core.BranchLedger `json:"ledger"`
	Executor core.Executor     `json:"executor"`
	Pool     core.Mempool      `json:"pool"`
}
type ImportConfig struct{}
type branchImporter struct {
	ImportDeps
	mu sync.Mutex
}

func init() { pluginkit.Register("importer/branch", NewImporter) }
func NewImporter(_ ImportConfig, d ImportDeps) (core.BlockImporter, error) {
	if d.Rules == nil || d.Ledger == nil || d.Executor == nil || d.Pool == nil {
		return nil, fmt.Errorf("import dependencies required")
	}
	if e := core.RequireCompatible(d.Rules.Protocol(), d.Ledger, d.Executor, d.Pool); e != nil {
		return nil, e
	}
	if d.Ledger.LedgerID() == nil {
		return nil, core.ErrLedgerMismatch
	}
	return &branchImporter{ImportDeps: d}, nil
}
func (p *branchImporter) Protocol() core.ProtocolDescriptor { return p.ImportDeps.Rules.Protocol() }
func (p *branchImporter) LedgerID() *core.LedgerIdentity    { return p.Ledger.LedgerID() }
func (p *branchImporter) Import(ctx context.Context, b core.Block, r *core.ExecutionResult) (returnErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	parent, e := p.Rules.Parent(b)
	if e != nil {
		return e
	}
	pb, e := p.Ledger.Block(ctx, parent)
	if e != nil {
		if onlyExpected(e, func(e error) bool { return e == core.ErrNotFound }) {
			return e
		}
		return localService(e)
	}
	if e = p.Rules.ValidateCandidate(ctx, supervisedChain{p.Ledger}, pb, b); e != nil {
		return e
	}
	var result core.ExecutionResult
	if r != nil {
		result = *r
	} else {
		snap, e := p.Ledger.Snapshot(ctx, parent)
		if e != nil {
			return localService(e)
		}
		result, e = p.Executor.Execute(ctx, snap, b)
		closeErr := snap.Close()
		if closeErr != nil {
			return localService(errors.Join(e, closeErr))
		}
		if e != nil {
			if expectedExecutionError(e) {
				return e
			}
			return localService(e)
		}
		result.BlockID = b.ID()
	}
	if e = p.Rules.ValidateBlock(ctx, supervisedChain{p.Ledger}, pb, b, result); e != nil {
		return e
	}
	change, e := p.Ledger.Import(ctx, parent, b, result)
	if e != nil {
		if expectedCommitError(e) {
			return e
		}
		return localService(e)
	}
	defer func() {
		if returnErr != nil {
			var committed *core.CommittedError
			if !errors.As(returnErr, &committed) {
				returnErr = &core.CommittedError{BlockID: b.ID(), Cause: returnErr}
			}
		}
	}()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	ctx = cleanup
	// Valid transactions on a stored noncanonical branch must remain eligible
	// for the local chain. Independent miners may never select a weaker branch.
	if change.OldHead == change.NewHead {
		return p.requeue(ctx, []core.Block{b}, nil)
	}
	attached := map[core.ID]bool{}
	var ids []core.ID
	for _, block := range change.Attached {
		txs, e := p.Rules.Transactions(block)
		if e != nil {
			return e
		}
		for _, tx := range txs {
			attached[tx.ID()] = true
			ids = append(ids, tx.ID())
		}
	}
	if e = p.Pool.Remove(ctx, ids); e != nil {
		return e
	}
	return p.requeue(ctx, change.Detached, attached)
}

// Detached is tip first; requeue ancestors first so dependent transactions are
// admitted in their original order. Canonical confirmations take precedence.
func (p *branchImporter) requeue(ctx context.Context, blocks []core.Block, attached map[core.ID]bool) error {
	for j := len(blocks) - 1; j >= 0; j-- {
		block := blocks[j]
		txs, e := p.Rules.Transactions(block)
		if e != nil {
			return e
		}
		for _, tx := range txs {
			_, confirmedErr := p.Ledger.Transaction(ctx, tx.ID())
			if confirmedErr != nil && !onlyExpected(confirmedErr, func(e error) bool { return e == core.ErrNotFound }) {
				return confirmedErr
			}
			if !attached[tx.ID()] && errors.Is(confirmedErr, core.ErrNotFound) {
				if e = p.Pool.Add(ctx, tx); e != nil && !onlyExpected(e, func(e error) bool { return e == core.ErrFull || e == core.ErrInvalidTransaction }) {
					return e
				}
			}
		}
	}
	return nil
}
