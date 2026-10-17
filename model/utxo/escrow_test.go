package utxo

import (
	"context"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"

	"strings"
	"testing"
)

func spendable(t *testing.T, state map[string][]byte, owner assetcap.Address) uint64 {
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
	owner := assetcap.Address{1}
	e, _ := New(Config{Network: "locked", Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
	state, _ := e.Genesis(ctx)
	locked, err := e.Lock(ctx, modeltest.Reader(state), assetcap.Lock{ID: "position", Owner: owner, Amount: 60})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, state, locked.Writes, locked.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if spendable(t, next, owner) != 40 {
		t.Fatal("stake remained spendable")
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), assetcap.Lock{ID: "too-much", Owner: owner, Amount: 50}); err == nil {
		t.Fatal("unfunded escrow accepted")
	}
	released, err := e.Release(ctx, modeltest.Reader(next), assetcap.Release{ID: "position", To: owner})
	if err != nil {
		t.Fatal(err)
	}
	next, err = core.ApplyStateChanges(ctx, next, released.Writes, released.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if spendable(t, next, owner) != 100 {
		t.Fatal("release changed supply")
	}
	if _, err := e.Release(ctx, modeltest.Reader(next), assetcap.Release{ID: "position", To: owner}); err == nil {
		t.Fatal("duplicate release accepted")
	}
	if _, err := e.Lock(ctx, modeltest.Reader(next), assetcap.Lock{ID: "position", Owner: owner, Amount: 1}); err == nil {
		t.Fatal("position ID reused")
	}
}
