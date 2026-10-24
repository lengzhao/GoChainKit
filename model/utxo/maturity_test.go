package utxo

import (
	"context"
	"crypto/ed25519"
	"errors"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"github.com/lengzhao/GoChainKit/model/asset"
	"testing"
)

type heightContext struct{ height uint64 }

func (h heightContext) Height() uint64              { return h.height }
func (h heightContext) Parent() core.Block          { return nil }
func (h heightContext) Candidate() core.Block       { return nil }
func (h heightContext) History() model.BlockHistory { return nil }

func TestCoinbaseMaturityBoundaryAndIsolation(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key[32:])
	e, err := New(Config{Network: "maturity", CoinbaseMaturity: 5})
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := e.Genesis(ctx)
	mint := assetcap.Mint{ID: "block-1", To: owner, Amount: 50}
	if _, err = e.Credit(ctx, modeltest.Reader(initial), mint); err == nil {
		t.Fatal("context-free mint accepted")
	}
	tr, err := e.CreditAtHeight(ctx, modeltest.Reader(initial), mint, 1)
	if err != nil {
		t.Fatal(err)
	}
	state, err := core.ApplyStateChanges(ctx, initial, tr.Writes, tr.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	coins, err := e.Coins(ctx, modeltest.Reader(state), owner, 5)
	if err != nil || len(coins) != 1 {
		t.Fatal(coins, err)
	}
	if coins[0].Spendable || coins[0].MatureHeight != 6 || !coins[0].Coinbase {
		t.Fatal(coins[0])
	}
	raw := Transaction{Network: "maturity", Inputs: []Input{{Previous: coins[0].Outpoint}}, Outputs: []Output{{To: owner, Amount: 49}}}
	if err = raw.Sign(key); err != nil {
		t.Fatal(err)
	}
	tx, err := Wrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx}); err == nil {
		t.Fatal("no-context spend accepted")
	}
	_, err = e.ExecuteInContext(ctx, modeltest.Reader(state), []core.Transaction{tx}, heightContext{5})
	var disposition *core.TransactionError
	if !errors.As(err, &disposition) || disposition.Disposition != core.TransactionDeferred {
		t.Fatal("immature reward not deferred", err)
	}
	tr, err = e.ExecuteInContext(ctx, modeltest.Reader(state), []core.Transaction{tx}, heightContext{6})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, state, tr.Writes, tr.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	coins, err = e.Coins(ctx, modeltest.Reader(next), owner, 6)
	if err != nil || len(coins) != 1 || coins[0].Amount != 49 || coins[0].Coinbase || !coins[0].Spendable {
		t.Fatal(coins, err)
	}
	if _, err = Lookup(ctx, modeltest.Reader(state), raw.Inputs[0].Previous); err != nil {
		t.Fatal("parent mutated", err)
	}
	if _, err = e.Lock(ctx, modeltest.Reader(state), assetcap.Lock{ID: "bypass", Owner: owner, Amount: 1}); err == nil {
		t.Fatal("escrow bypassed maturity")
	}
	other, _ := New(Config{Network: "maturity", CoinbaseMaturity: 6})
	if e.Protocol() == other.Protocol() {
		t.Fatal("maturity not protocol-bound")
	}
}

func TestCoinsOrderedAndOverflow(t *testing.T) {
	ctx := context.Background()
	e, _ := New(Config{Network: "listing", CoinbaseMaturity: 5})
	owner := assetcap.Address{1}
	state, _ := e.Genesis(ctx)
	if _, err := e.CreditAtHeight(ctx, modeltest.Reader(state), assetcap.Mint{ID: "overflow", To: owner, Amount: 1}, ^uint64(0)-2); err == nil {
		t.Fatal("maturity overflow accepted")
	}
	for _, id := range []core.ID{"a", "b", "c"} {
		tr, err := e.CreditAtHeight(ctx, modeltest.Reader(state), assetcap.Mint{ID: id, To: owner, Amount: 7}, 1)
		if err != nil {
			t.Fatal(err)
		}
		state, _ = core.ApplyStateChanges(ctx, state, tr.Writes, tr.Deletes)
	}
	a, err := e.Coins(ctx, modeltest.Reader(state), owner, 6)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Coins(ctx, modeltest.Reader(state), assetcap.Address{2}, 6)
	if err != nil || len(b) != 0 {
		t.Fatal(b, err)
	}
	if len(a) != 3 {
		t.Fatal(a)
	}
	for i := 1; i < len(a); i++ {
		if string(a[i-1].Outpoint.TransactionID[:]) >= string(a[i].Outpoint.TransactionID[:]) {
			t.Fatal("unordered coins")
		}
	}
}
