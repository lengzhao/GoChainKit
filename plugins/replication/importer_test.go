package replication

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"testing"
)

type importRules struct{ ImportProtocol }

func (importRules) Parent(core.Block) (core.ID, error) { return "parent", nil }
func (importRules) ValidateCandidate(context.Context, core.ChainStore, core.Block, core.Block) error {
	return nil
}

type snapshotLedger struct {
	core.BranchLedger
	reader    core.StateReader
	importErr error
}

func (snapshotLedger) Block(context.Context, core.ID) (core.Block, error) {
	return powTestBlock("parent"), nil
}
func (l snapshotLedger) Snapshot(context.Context, core.ID) (core.StateReader, error) {
	return l.reader, nil
}

type errorReader struct {
	core.StateReader
	failure error
}

func (r errorReader) Close() error { return r.failure }

type errorExecutor struct {
	core.Executor
	failure error
}

func (e errorExecutor) Execute(context.Context, core.StateReader, core.Block) (core.ExecutionResult, error) {
	return core.ExecutionResult{}, e.failure
}
func TestImporterMixedExecutionErrorRemainsLocal(t *testing.T) {
	disk := errors.New("execution database failure")
	for _, signal := range []error{context.Canceled, core.ErrInvalidTransaction, core.ErrDeferredTransaction} {
		imp := &branchImporter{ImportDeps: ImportDeps{Rules: importRules{}, Ledger: snapshotLedger{reader: errorReader{}}, Executor: errorExecutor{failure: errors.Join(signal, disk)}}}
		err := imp.Import(context.Background(), powTestBlock("candidate"), nil)
		var local *core.LocalServiceError
		if !errors.As(err, &local) || !errors.Is(err, disk) {
			t.Fatalf("mixed %v lost local failure: %v", signal, err)
		}
	}
}
func TestImporterExecutionCancellationPreservesCloseFailure(t *testing.T) {
	disk := errors.New("snapshot close failed")
	imp := &branchImporter{ImportDeps: ImportDeps{Rules: importRules{}, Ledger: snapshotLedger{reader: errorReader{failure: disk}}, Executor: errorExecutor{failure: context.Canceled}}}
	err := imp.Import(context.Background(), powTestBlock("candidate"), nil)
	var local *core.LocalServiceError
	if !errors.As(err, &local) || !errors.Is(err, disk) {
		t.Fatalf("close error lost during canceled execution: %v", err)
	}
}

// This adapter deliberately omits Work and Seal. Shared import logic must not
// require an algorithm-specific capability when validation is already provided.
type protocolContract = core.Protocol
type nonWorkBranch struct {
	protocolContract
	parent func(core.Block) (core.ID, error)
}

func (p nonWorkBranch) Parent(b core.Block) (core.ID, error) { return p.parent(b) }
func TestImporterRecoversValidatedSideBranchTransaction(t *testing.T) {
	ctx := context.Background()
	p, e := pow.New(pow.Config{ChainID: "side-transaction"})
	if e != nil {
		t.Fatal(e)
	}
	base, _ := kv.New(kv.Config{ChainID: p.Protocol().Network})
	inner, _ := kv.NewExecutor(base)
	executor, _ := pow.NewExecutor(p, inner)
	build := func(pool core.Mempool, ledger core.BranchLedger) (core.Block, core.ExecutionResult) {
		sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
		b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: pool, Executor: executor, Selector: sel})
		if e != nil {
			t.Fatal(e)
		}
		h, _ := ledger.Head(ctx)
		block, result, e := b.Build(ctx, h)
		if e != nil {
			t.Fatal(e)
		}
		return block, result
	}
	local, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	side, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	sidePool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	a, result := build(pool, local)
	if _, e = local.Import(ctx, result.Parent, a, result); e != nil {
		t.Fatal(e)
	}
	tx := pow.Transaction{ChainID: p.Protocol().Network, Nonce: 1, Key: []byte("key"), Value: []byte("side")}
	if e = tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); e != nil {
		t.Fatal(e)
	}
	wrapped, _ := pow.WrapTransaction(tx)
	if e = sidePool.Add(ctx, wrapped); e != nil {
		t.Fatal(e)
	}
	b, _ := build(sidePool, side)
	rules := nonWorkBranch{protocolContract: p, parent: p.Parent}
	if _, ok := any(rules).(core.WorkProtocol); ok {
		t.Fatal("test adapter leaks Work")
	}
	imp, e := NewImporter(ImportConfig{}, ImportDeps{Rules: rules, Ledger: local, Executor: executor, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	if e = imp.Import(ctx, b, nil); e != nil {
		t.Fatal(e)
	}
	h, _ := local.Head(ctx)
	if h != a.ID() {
		t.Fatal("equal score switched canonical head")
	}
	candidates, e := pool.Candidates(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(candidates) != 1 || candidates[0].ID() != wrapped.ID() {
		t.Fatal("valid side-branch transfer not available to local miner")
	}
	if e = imp.Import(ctx, b, nil); e != nil && !errors.Is(e, core.ErrAlreadyCommitted) {
		t.Fatal(e)
	}
	candidates, _ = pool.Candidates(ctx)
	if len(candidates) != 1 {
		t.Fatal("duplicate side import duplicated pool entry")
	}
	// Once this transfer is canonical, another valid side block carrying the
	// same transfer must not reintroduce it into the pool.
	canonical, canonicalResult := build(pool, local)
	if e = imp.Import(ctx, canonical, &canonicalResult); e != nil {
		t.Fatal(e)
	}
	// Construct a distinct valid proof over the same body, rather than relying
	// on randomized Seal to differ or accepting an already-stored ID.
	different, e := pow.UnwrapBlock(b)
	if e != nil {
		t.Fatal(e)
	}
	var again core.Block
	for tries := 0; tries < 1<<20; tries++ {
		different.Nonce++
		candidate, err := pow.WrapBlock(different)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Work(candidate); err == nil {
			again = candidate
			break
		}
	}
	if again == nil || again.ID() == b.ID() {
		t.Fatal("did not construct a distinct valid side block")
	}
	if _, e = local.Block(ctx, again.ID()); !errors.Is(e, core.ErrNotFound) {
		t.Fatalf("replacement side block already stored: %v", e)
	}
	if e = imp.Import(ctx, again, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = local.Block(ctx, again.ID()); e != nil {
		t.Fatal("new side block not imported", e)
	}
	candidates, _ = pool.Candidates(ctx)
	if len(candidates) != 0 {
		t.Fatal("canonical transfer recovered from side branch")
	}
	// A side block is durably saved even when its subsequent pool maintenance
	// fails. This must remain observable as a committed, non-retryable fault.
	local2, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = local2.Import(ctx, result.Parent, a, result); e != nil {
		t.Fatal(e)
	}
	failure := errors.New("side transaction recovery failed")
	broken, e := NewImporter(ImportConfig{}, ImportDeps{Rules: rules, Ledger: local2, Executor: executor, Pool: failingAddPool{Mempool: pool, failure: failure}})
	if e != nil {
		t.Fatal(e)
	}
	e = broken.Import(ctx, b, nil)
	var committed *core.CommittedError
	if !errors.As(e, &committed) || !errors.Is(e, failure) || committed.BlockID != b.ID() {
		t.Fatalf("lost committed side maintenance fault: %v", e)
	}
	if _, e = local2.Block(ctx, b.ID()); e != nil {
		t.Fatal("side block not durably saved", e)
	}
	h, _ = local2.Head(ctx)
	if h != a.ID() {
		t.Fatal("side maintenance failure changed canonical head")
	}
}

type failingAddPool struct {
	core.Mempool
	failure error
}

func (p failingAddPool) Add(context.Context, core.Transaction) error { return p.failure }

type parentLookupFault struct {
	core.BranchLedger
	failure error
}

func (l parentLookupFault) Block(context.Context, core.ID) (core.Block, error) { return nil, l.failure }
func TestImporterMixedMissingParentIsLocal(t *testing.T) {
	failure := errors.New("disk read failed")
	imp := &branchImporter{ImportDeps: ImportDeps{Rules: importRules{}, Ledger: parentLookupFault{failure: errors.Join(core.ErrNotFound, failure)}}}
	err := imp.Import(context.Background(), powTestBlock("candidate"), nil)
	var local *core.LocalServiceError
	if !errors.As(err, &local) || !errors.Is(err, failure) {
		t.Fatalf("local parent lookup fault hidden by NotFound: %v", err)
	}
}

type historyLookupFault struct {
	core.ChainStore
	failure error
}

func (l historyLookupFault) Head(context.Context) (core.ID, error) { return "", l.failure }
func (l historyLookupFault) Block(context.Context, core.ID) (core.Block, error) {
	return nil, l.failure
}
func (l historyLookupFault) Receipt(context.Context, core.ID) (core.Receipt, error) {
	return core.Receipt{}, l.failure
}
func TestSupervisedHistoryMixedMissingRemainsLocal(t *testing.T) {
	failure := errors.New("history database failed")
	history := supervisedChain{ChainStore: historyLookupFault{failure: errors.Join(core.ErrNotFound, failure)}}
	_, headErr := history.Head(context.Background())
	_, blockErr := history.Block(context.Background(), "b")
	_, receiptErr := history.Receipt(context.Background(), "tx")
	for _, err := range []error{headErr, blockErr, receiptErr} {
		var local *core.LocalServiceError
		if !errors.As(err, &local) || !errors.Is(err, failure) {
			t.Fatalf("local history error hidden by NotFound: %v", err)
		}
	}
}
