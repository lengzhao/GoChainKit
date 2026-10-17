package account

import (
	"context"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/asset"
	"testing"
)

func TestEscrowLocksRealBalanceAndReleasesOnce(t *testing.T) {
	ctx := context.Background()
	owner := asset.Address{1}
	e, _ := New(Config{Network: "locked", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
	state, _ := e.Genesis(ctx)
	locked, err := e.Lock(ctx, modeltest.Reader(state), asset.Lock{ID: "position", Owner: owner, Amount: 60})
	if err != nil {
		t.Fatal(err)
	}
	next, err := model.Apply(ctx, state, locked)
	if err != nil {
		t.Fatal(err)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), owner); balance != 40 {
		t.Fatal("stake remained spendable", balance)
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), asset.Lock{ID: "too-much", Owner: owner, Amount: 50}); err == nil {
		t.Fatal("unfunded stake accepted")
	}
	released, err := e.Release(ctx, modeltest.Reader(next), asset.Release{ID: "position", To: owner})
	if err != nil {
		t.Fatal(err)
	}
	next, err = model.Apply(ctx, next, released)
	if err != nil {
		t.Fatal(err)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), owner); balance != 100 {
		t.Fatal("release changed supply", balance)
	}
	if _, err := e.Release(ctx, modeltest.Reader(next), asset.Release{ID: "position", To: owner}); err == nil {
		t.Fatal("duplicate release accepted")
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), asset.Lock{ID: "position", Owner: owner, Amount: 1}); err == nil {
		t.Fatal("position ID reused")
	}
}
