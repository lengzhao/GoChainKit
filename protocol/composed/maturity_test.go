package composed_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/pow"
	"path/filepath"
	"testing"
	"time"
)

func TestMatureCoinbaseFeesReorgAndReopen(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key[32:])
	recipient := assetcap.Address{9}
	engine, _ := utxo.New(utxo.Config{Network: "mature-reorg", CoinbaseMaturity: 2})
	clock := int64(1700000000)
	rule, err := pow.New(pow.Config{RetargetInterval: 100, InitialReward: 50, Clock: func() time.Time { return time.Unix(clock+100, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	p, err := composed.NewPoW(composed.Config{Network: "mature-reorg", GenesisTime: clock, Producer: owner[:], Clock: func() time.Time { return time.Unix(clock, 0) }}, engine, rule, rule)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chain.db")
	s, err := storage.NewKV(storage.KVConfig{}, storage.KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(ctx) }()
	executor, err := composed.NewExecutor(p, s)
	if err != nil {
		t.Fatal(err)
	}
	build := func(parent core.ID, txs ...core.Transaction) core.Block {
		t.Helper()
		clock++
		previous, err := s.Block(ctx, parent)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := p.Draft(ctx, previous, txs)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := s.Snapshot(ctx, parent)
		if err != nil {
			t.Fatal(err)
		}
		result, err := executor.Execute(ctx, snapshot, draft)
		snapshot.Close()
		if err != nil {
			t.Fatal(err)
		}
		block, err := p.Finalize(ctx, draft, result)
		if err != nil {
			t.Fatal(err)
		}
		block, err = p.Seal(ctx, block)
		if err != nil {
			t.Fatal(err)
		}
		result.BlockID = block.ID()
		if _, err = s.Import(ctx, parent, block, result); err != nil {
			t.Fatal(err)
		}
		return block
	}
	g, _ := s.Head(ctx)
	a := build(g)
	b := build(a.ID())
	snap, _ := s.Snapshot(ctx, a.ID())
	coins, err := engine.Coins(ctx, snap, owner, 3)
	snap.Close()
	if err != nil || len(coins) != 1 {
		t.Fatal(coins, err)
	}
	raw := utxo.Transaction{Network: "mature-reorg", Inputs: []utxo.Input{{Previous: coins[0].Outpoint}}, Outputs: []utxo.Output{{To: recipient, Amount: 10}, {To: owner, Amount: 39}}}
	if err = raw.Sign(key); err != nil {
		t.Fatal(err)
	}
	tx, err := utxo.Wrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The composed executor must propagate model errors. In particular an
	// immature transaction cannot be confirmed with an empty transition.
	premature, err := p.Draft(ctx, a, []core.Transaction{tx})
	if err != nil {
		t.Fatal(err)
	}
	immatureSnapshot, err := s.Snapshot(ctx, a.ID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(ctx, immatureSnapshot, premature)
	immatureSnapshot.Close()
	var disposition *core.TransactionError
	if !errors.As(err, &disposition) || disposition.Disposition != core.TransactionDeferred {
		t.Fatalf("contextual immature execution error lost: %v", err)
	}
	c := build(b.ID(), tx)
	snap, _ = s.Snapshot(ctx, c.ID())
	minerCoins, err := engine.Coins(ctx, snap, owner, 4)
	snap.Close()
	if err != nil {
		t.Fatal(err)
	}
	foundFeeReward := false
	for _, coin := range minerCoins {
		if coin.Coinbase && coin.CreatedHeight == 3 && coin.Amount == 51 && coin.MatureHeight == 5 {
			foundFeeReward = true
		}
	}
	if !foundFeeReward {
		t.Fatal("subsidy plus fee missing", minerCoins)
	}
	fork := build(g)
	for i := 0; i < 3; i++ {
		fork = build(fork.ID())
	}
	head, _ := s.Head(ctx)
	if head != fork.ID() {
		t.Fatal("heavier fork not selected")
	}
	if _, err = s.Transaction(ctx, tx.ID()); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("detached transfer still canonical", err)
	}
	snap, _ = s.Snapshot(ctx, head)
	received, err := engine.Coins(ctx, snap, recipient, 5)
	snap.Close()
	if err != nil || len(received) != 0 {
		t.Fatal("detached output survives", received, err)
	}
	if err = s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s, err = storage.NewKV(storage.KVConfig{}, storage.KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if reopened, _ := s.Head(ctx); reopened != head {
		t.Fatal("head lost")
	}
	executor, err = composed.NewExecutor(p, s)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = s.Snapshot(ctx, head)
	after, err := engine.Coins(ctx, snap, owner, 5)
	snap.Close()
	if err != nil || len(after) != 4 {
		t.Fatal(after, err)
	}
	for _, coin := range after {
		if coin.Spendable != (coin.MatureHeight <= 5) {
			t.Fatal("maturity metadata lost", coin)
		}
	}
	_ = build(head)
}
