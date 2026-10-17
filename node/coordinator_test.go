package node

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	store "github.com/lengzhao/GoChainKit/plugins/storage"
	"testing"
)

type failingCommitter struct {
	core.Ledger
	err error
}

func (f *failingCommitter) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	if f.err != nil {
		return f.err
	}
	return f.Ledger.Commit(ctx, parent, b, r)
}
func TestCommitOrderingAndProtocolValidation(t *testing.T) {
	ctx := context.Background()
	protocol := testprotocol.New("custom")
	s, err := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	p, err := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, testprotocol.Tx{Label: "a", Body: []byte("value")}); err != nil {
		t.Fatal(err)
	}
	e := testprotocol.Executor{P: protocol}
	selection, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: protocol})
	b, err := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: protocol, Pool: p, Ledger: s, Selector: selection, Executor: e})
	if err != nil {
		t.Fatal(err)
	}
	commit := &failingCommitter{Ledger: s}
	h, err := NewCoordinator(CoordinatorDeps{Protocol: protocol, Builder: b, Ledger: commit, Executor: e, Pool: p})
	if err != nil {
		t.Fatal(err)
	}
	block, err := h.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := block.(testprotocol.Frame)
	bad.Commitment = "forged"
	if err := h.Validate(ctx, bad); err == nil {
		t.Fatal("accepted invalid commitment")
	}
	failure := errors.New("commit failed")
	commit.err = failure
	if err := h.Commit(ctx, block); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	left, _ := p.Candidates(ctx)
	if len(left) != 1 {
		t.Fatal("removed before commit")
	}
	commit.err = nil
	if err := h.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	left, _ = p.Candidates(ctx)
	if len(left) != 0 {
		t.Fatal("not removed")
	}
	if err := h.Commit(ctx, block); !errors.Is(err, core.ErrConflict) {
		t.Fatal("accepted stale block", err)
	}
}

type failingPool struct {
	core.Mempool
	err error
}

func (p failingPool) Remove(context.Context, []core.ID) error { return p.err }
func TestLedgerIdentityAndCommittedCleanupError(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("identity")
	a, _ := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	other, _ := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	pool, _ := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: p})
	pool.Add(ctx, testprotocol.Tx{Label: "one", Body: []byte("v")})
	e := testprotocol.Executor{P: p}
	selection, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, err := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: a, Pool: pool, Executor: e, Selector: selection})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewCoordinator(CoordinatorDeps{Protocol: p, Ledger: other, Builder: b, Executor: e, Pool: pool}); !errors.Is(err, core.ErrLedgerMismatch) {
		t.Fatal("mixed ledgers accepted", err)
	}
	failure := errors.New("cleanup failed")
	h, err := NewCoordinator(CoordinatorDeps{Protocol: p, Ledger: a, Builder: b, Executor: e, Pool: failingPool{pool, failure}})
	if err != nil {
		t.Fatal(err)
	}
	block, err := h.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = h.Commit(ctx, block)
	var committed *core.CommittedError
	if !errors.As(err, &committed) || committed.BlockID != block.ID() || !errors.Is(err, failure) {
		t.Fatal("commit result unclear", err)
	}
	head, _ := a.Head(ctx)
	if head != block.ID() {
		t.Fatal("block not committed")
	}
}

type rejectStaleExecutor struct {
	core.Executor
	calls int
}

func (e *rejectStaleExecutor) Execute(ctx context.Context, s core.StateReader, b core.Block) (core.ExecutionResult, error) {
	e.calls++
	if e.calls > 2 {
		return core.ExecutionResult{}, core.ErrInvalidTransaction
	}
	return e.Executor.Execute(ctx, s, b)
}
func TestTwoCoordinatorsRejectStaleBeforeExecution(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("shared")
	ledger, _ := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	m, _ := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: p})
	m.Add(ctx, testprotocol.Tx{Label: "one", Body: []byte("v")})
	e := &rejectStaleExecutor{Executor: testprotocol.Executor{P: p}}
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, err := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: m, Executor: e, Selector: sel})
	if err != nil {
		t.Fatal(err)
	}
	deps := CoordinatorDeps{Protocol: p, Ledger: ledger, Builder: b, Executor: e, Pool: m}
	a, _ := NewCoordinator(deps)
	other, _ := NewCoordinator(deps)
	candidate, err := a.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Commit(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	calls := e.calls
	for _, check := range []func(context.Context, core.Block) error{a.Validate, a.Commit} {
		if err = check(ctx, candidate); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("stale candidate should conflict before execution: %v", err)
		}
	}
	if e.calls != calls {
		t.Fatal("stale candidate executed against new state")
	}
}

type cancelAfterCommit struct {
	core.Ledger
	cancel context.CancelFunc
}

func (l cancelAfterCommit) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	err := l.Ledger.Commit(ctx, parent, b, r)
	if err == nil {
		l.cancel()
	}
	return err
}
func TestCommittedCleanupSurvivesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := testprotocol.New("shutdown")
	ledger, _ := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	mem, _ := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: p})
	if err := mem.Add(ctx, testprotocol.Tx{Label: "one", Body: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	selector, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	e := testprotocol.Executor{P: p}
	builder, _ := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: mem, Executor: e, Selector: selector})
	host, err := NewCoordinator(CoordinatorDeps{Protocol: p, Ledger: cancelAfterCommit{ledger, cancel}, Pool: mem, Executor: e, Builder: builder})
	if err != nil {
		t.Fatal(err)
	}
	block, err := host.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	left, err := mem.Candidates(context.Background())
	if err != nil || len(left) != 0 {
		t.Fatal("committed transaction retained", err)
	}
}
