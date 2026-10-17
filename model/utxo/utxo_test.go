package utxo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/asset"
	"testing"
)

func TestSpendDeletesInputPreservesChangeAndPaysFee(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key.Public().(ed25519.PublicKey))
	recipient := asset.Address{1}
	e, err := New(Config{Network: "utxos", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.Genesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := e.GenesisOutpoints()[0]
	raw := Transaction{Network: "utxos", Inputs: []Input{{Previous: input}}, Outputs: []Output{{To: recipient, Amount: 30}, {To: owner, Amount: 68}}}
	if err := raw.Sign(key); err != nil {
		t.Fatal(err)
	}
	tx, err := Wrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	change, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx})
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Deletes) != 1 {
		t.Fatal("input not explicitly deleted")
	}
	next, err := model.Apply(ctx, state, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(ctx, modeltest.Reader(next), input); err == nil {
		t.Fatal("spent output remains")
	}
	if _, err := Lookup(ctx, modeltest.Reader(state), input); err != nil {
		t.Fatal("parent changed", err)
	}
	outputs, err := Outpoints(tx)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []uint64{30, 68} {
		o, err := Lookup(ctx, modeltest.Reader(next), outputs[i])
		if err != nil || o.Amount != want {
			t.Fatal(o, err)
		}
	}
	fee, err := asset.Fees(change)
	if err != nil || fee != 2 {
		t.Fatal(fee, err)
	}
	if _, err := e.Execute(ctx, modeltest.Reader(next), []core.Transaction{tx}); err == nil {
		t.Fatal("double spend accepted")
	}
	if _, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx, tx}); err == nil {
		t.Fatal("in-block double spend accepted")
	}
	encoded, err := e.EncodeTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := e.DecodeTransaction(encoded)
	if err != nil || copy.ID() != tx.ID() {
		t.Fatal(err)
	}
}

func TestChainedSpendAndUnauthorizedInput(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key.Public().(ed25519.PublicKey))
	e, _ := New(Config{Network: "chain", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
	state, _ := e.Genesis(ctx)
	first := Transaction{Network: "chain", Inputs: []Input{{Previous: e.GenesisOutpoints()[0]}}, Outputs: []Output{{To: owner, Amount: 98}}}
	first.Sign(key)
	tx1, _ := Wrap(first)
	points, _ := Outpoints(tx1)
	second := Transaction{Network: "chain", Inputs: []Input{{Previous: points[0]}}, Outputs: []Output{{To: asset.Address{2}, Amount: 95}}}
	second.Sign(key)
	tx2, _ := Wrap(second)
	change, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx1, tx2})
	if err != nil {
		t.Fatal(err)
	}
	next, err := model.Apply(ctx, state, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(ctx, modeltest.Reader(next), points[0]); err == nil {
		t.Fatal("intermediate output retained")
	}
	if fees, _ := asset.Fees(change); fees != 5 {
		t.Fatal(fees)
	}
	attackerSeed := make([]byte, 32)
	attackerSeed[0] = 1
	attacker := ed25519.NewKeyFromSeed(attackerSeed)
	bad := first
	bad.Sign(attacker)
	forged, _ := Wrap(bad)
	if _, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{forged}); err == nil {
		t.Fatal("wrong owner accepted")
	}
	overspend := first
	overspend.Outputs = []Output{{To: owner, Amount: 101}}
	overspend.Sign(key)
	inflation, _ := Wrap(overspend)
	if _, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{inflation}); err == nil {
		t.Fatal("inflation accepted")
	}
}

func TestRepeatedSpendOfIntermediateOutputIsRejected(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key.Public().(ed25519.PublicKey))
	e, _ := New(Config{Network: "intermediate", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
	state, _ := e.Genesis(ctx)
	first := Transaction{Network: "intermediate", Inputs: []Input{{Previous: e.GenesisOutpoints()[0]}}, Outputs: []Output{{To: owner, Amount: 98}}}
	first.Sign(key)
	tx1, _ := Wrap(first)
	points, _ := Outpoints(tx1)
	second := Transaction{Network: "intermediate", Inputs: []Input{{Previous: points[0]}}, Outputs: []Output{{To: owner, Amount: 95}}}
	second.Sign(key)
	tx2, _ := Wrap(second)
	third := Transaction{Network: "intermediate", Inputs: []Input{{Previous: points[0]}}, Outputs: []Output{{To: asset.Address{3}, Amount: 94}}}
	third.Sign(key)
	tx3, _ := Wrap(third)
	_, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx1, tx2, tx3})
	if !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatal("known double spend not rejected", err)
	}
}
