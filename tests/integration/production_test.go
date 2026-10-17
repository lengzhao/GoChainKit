package integration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"github.com/lengzhao/GoChainKit/node"
	builder "github.com/lengzhao/GoChainKit/plugins/builder/basic"
	consensus "github.com/lengzhao/GoChainKit/plugins/consensus/single"
	executor "github.com/lengzhao/GoChainKit/plugins/executor/kv"
	mempool "github.com/lengzhao/GoChainKit/plugins/mempool/memory"
	storage "github.com/lengzhao/GoChainKit/plugins/storage/memory"
	"testing"
	"time"
)

type stopAtHeight struct {
	contracts.ConsensusHost
	cancel context.CancelFunc
}

func (h stopAtHeight) Commit(ctx context.Context, b types.Block) error {
	if err := h.ConsensusHost.Commit(ctx, b); err != nil {
		return err
	}
	if b.Header.Height >= 3 {
		h.cancel()
	}
	return nil
}

func TestSingleNodeProduction(t *testing.T) {
	ctx := context.Background()
	s, err := storage.New(storage.Config{ChainID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := mempool.New(mempool.Config{ChainID: "dev", MaxBytes: 4096, MaxTransactions: 10})
	if err != nil {
		t.Fatal(err)
	}
	e, err := executor.New(executor.Config{ChainID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := builder.New(builder.Config{ChainID: "dev", MaxTransactionBytes: 4096}, builder.Deps{Pool: p, Chain: s, States: s, Executor: e})
	if err != nil {
		t.Fatal(err)
	}
	h, err := node.NewCoordinator(node.CoordinatorDeps{Builder: b, Chain: s, States: s, Executor: e, Committer: s, Pool: p})
	if err != nil {
		t.Fatal(err)
	}
	tx := types.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("hello"), Value: []byte("world")}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	proposed, err := h.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(ctx, proposed); err != nil {
		t.Fatal(err)
	}
	genesisHead, _ := s.Head(ctx)
	_, isolated, err := b.Build(ctx, genesisHead)
	if err != nil {
		t.Fatal(err)
	}
	forged := isolated
	forged.Writes = map[string][]byte{"forged": []byte("state")}
	if err := s.Commit(ctx, genesisHead, proposed, forged); err == nil {
		t.Fatal("accepted inconsistent writes")
	}
	unchanged, _ := s.Head(ctx)
	if unchanged != genesisHead {
		t.Fatal("failed commit advanced head")
	}
	before, err := s.Snapshot(ctx, genesisHead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.Get(ctx, executor.ValueKey(tx.Sender(), tx.Key)); !errors.Is(err, contracts.ErrNotFound) {
		t.Fatal("execution mutated parent", err)
	}
	_ = before.Close()
	if err := h.Commit(ctx, proposed); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, genesisHead, proposed, isolated); !errors.Is(err, contracts.ErrAlreadyCommitted) {
		t.Fatal("duplicate commit", err)
	}
	head, err := s.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	block, err := s.Block(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if block.Header.Height != 1 || len(block.Transactions) != 1 {
		t.Fatal("transaction not included")
	}
	id, _ := tx.ID()
	r, err := s.Receipt(ctx, id)
	if err != nil || !r.Success {
		t.Fatal("missing receipt", err)
	}
	snapshot, err := s.Snapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	value, err := snapshot.Get(ctx, executor.ValueKey(tx.Sender(), tx.Key))
	if err != nil || string(value) != "world" {
		t.Fatal("state not applied", err)
	}
	parentState, err := s.Snapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(ctx, parentState, types.Block{Header: types.BlockHeader{ChainID: "dev", Parent: head}, Transactions: []types.Transaction{tx}})
	_ = parentState.Close()
	if !errors.Is(err, contracts.ErrInvalidTransaction) {
		t.Fatal("accepted replayed nonce", err)
	}
	remaining, err := p.Select(ctx, 4096)
	if err != nil || len(remaining) != 0 {
		t.Fatal("transaction not removed", err)
	}
	c, err := consensus.New(consensus.Config{Interval: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	deadline, timeout := context.WithTimeout(ctx, 2*time.Second)
	defer timeout()
	run, cancel := context.WithCancel(deadline)
	defer cancel()
	if err := c.Run(run, stopAtHeight{h, cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	next, _ := s.Head(ctx)
	last, _ := s.Block(ctx, next)
	if last.Header.Height <= 1 {
		t.Fatal("timer did not produce empty blocks")
	}
}
