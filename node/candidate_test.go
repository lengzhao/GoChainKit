package node

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/executor"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	store "github.com/lengzhao/GoChainKit/plugins/storage"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
	"testing"
)

func TestKVStaleNonceCandidateConflictsBeforeExecution(t *testing.T) {
	ctx := context.Background()
	p, _ := kv.New(kv.Config{ChainID: "dev"})
	ledger, _ := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	m, _ := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: p})
	tx := kv.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("k"), Value: []byte("v")}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	w, _ := kv.WrapTransaction(tx)
	if err := m.Add(ctx, w); err != nil {
		t.Fatal(err)
	}
	e, _ := executor.NewKV(executor.KVConfig{}, executor.Deps{Protocol: p})
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
	for _, check := range []func(context.Context, core.Block) error{a.Validate, a.Commit} {
		if err = check(ctx, candidate); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("stale nonce candidate: %v", err)
		}
	}
	// Reusing nonce one against the actual current parent remains a transaction
	// rejection, rather than being reclassified as a retryable parent conflict.
	head, _ := ledger.Head(ctx)
	parent, _ := ledger.Block(ctx, head)
	fresh, err := p.Draft(ctx, parent, []core.Transaction{w})
	if err != nil {
		t.Fatal(err)
	}
	err = a.Validate(ctx, fresh)
	var rejected *core.TransactionError
	if !errors.As(err, &rejected) || rejected.Disposition != core.TransactionRejected || errors.Is(err, core.ErrConflict) {
		t.Fatalf("real transaction error lost: %v", err)
	}
}
