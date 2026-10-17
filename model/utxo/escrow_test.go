package utxo

import (
	"context"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/asset"
	"strings"
	"testing"
)

func spendable(t *testing.T, state map[string][]byte, owner asset.Address) uint64 {
	t.Helper()
	var amount uint64
	for k, data := range state {
		if strings.HasPrefix(k, "model/utxo/output/") {
			coin, err := decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if coin.To == owner {
				amount += coin.Amount
			}
		}
	}
	return amount
}
func TestEscrowConsumesOutputsAndReleasesOnce(t *testing.T) {
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
	if spendable(t, next, owner) != 40 {
		t.Fatal("stake remained spendable")
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), asset.Lock{ID: "too-much", Owner: owner, Amount: 50}); err == nil {
		t.Fatal("unfunded escrow accepted")
	}
	released, err := e.Release(ctx, modeltest.Reader(next), asset.Release{ID: "position", To: owner})
	if err != nil {
		t.Fatal(err)
	}
	next, err = model.Apply(ctx, next, released)
	if err != nil {
		t.Fatal(err)
	}
	if spendable(t, next, owner) != 100 {
		t.Fatal("release changed supply")
	}
	if _, err := e.Release(ctx, modeltest.Reader(next), asset.Release{ID: "position", To: owner}); err == nil {
		t.Fatal("duplicate release accepted")
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), asset.Lock{ID: "position", Owner: owner, Amount: 1}); err == nil {
		t.Fatal("position ID reused")
	}
}
