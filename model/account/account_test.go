package account

import (
	"context"
	"crypto/ed25519"
	"errors"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"github.com/lengzhao/GoChainKit/model/asset"
	"testing"
)

func TestTransferFeesRewardsAndReplay(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	sender := asset.AddressOf(key.Public().(ed25519.PublicKey))
	recipient := assetcap.Address{1}
	miner := assetcap.Address{2}
	e, err := New(Config{Network: "accounts", Allocations: []assetcap.Allocation{{Address: sender, Amount: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.Genesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := Transaction{Network: "accounts", Nonce: 1, To: recipient, Amount: 30, Fee: 2}
	if err := raw.Sign(key); err != nil {
		t.Fatal(err)
	}
	tx, err := Wrap(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, state, result.Writes, result.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), sender); balance != 68 {
		t.Fatal(balance)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), recipient); balance != 30 {
		t.Fatal(balance)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(state), sender); balance != 100 {
		t.Fatal("parent mutated")
	}
	fees, err := asset.Fees(result)
	if err != nil || fees != 2 {
		t.Fatal(fees, err)
	}
	minted, err := e.Credit(ctx, modeltest.Reader(next), assetcap.Mint{ID: "reward", To: miner, Amount: fees})
	if err != nil {
		t.Fatal(err)
	}
	next, err = core.ApplyStateChanges(ctx, next, minted.Writes, minted.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), miner); balance != 2 {
		t.Fatal(balance)
	}
	if _, err := e.Execute(ctx, modeltest.Reader(next), []core.Transaction{tx}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatal("replay accepted", err)
	}
	if _, err := e.Credit(ctx, modeltest.Reader(next), assetcap.Mint{ID: "reward", To: miner, Amount: fees}); err == nil {
		t.Fatal("duplicate mint accepted")
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

func TestMintNamespaceCannotOverlapAccountAddress(t *testing.T) {
	ctx := context.Background()
	e, _ := New(Config{Network: "namespace"})
	state, _ := e.Genesis(ctx)
	id := core.ID("123456789012345678901234567")
	var recipient assetcap.Address
	copy(recipient[:], []byte("mint/"+string(id)))
	change, err := e.Credit(ctx, modeltest.Reader(state), assetcap.Mint{ID: id, To: recipient, Amount: 7})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, state, change.Writes, change.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	balance, err := Balance(ctx, modeltest.Reader(next), recipient)
	if err != nil || balance != 7 {
		t.Fatalf("mint marker corrupted account: %d %v", balance, err)
	}
}

func TestSelfTransferAndInvalidStateTransitions(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key.Public().(ed25519.PublicKey))
	e, _ := New(Config{Network: "self", Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
	state, _ := e.Genesis(ctx)
	raw := Transaction{Network: "self", Nonce: 1, To: owner, Amount: 50, Fee: 3}
	raw.Sign(key)
	tx, _ := Wrap(raw)
	result, err := e.Execute(ctx, modeltest.Reader(state), []core.Transaction{tx})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.ApplyStateChanges(ctx, state, result.Writes, result.Deletes)
	if err != nil {
		t.Fatal(err)
	}
	if balance, _ := Balance(ctx, modeltest.Reader(next), owner); balance != 97 {
		t.Fatal("self transfer changed principal", balance)
	}
	raw.Nonce = 2
	raw.Amount = 100
	raw.Sign(key)
	unfunded, _ := Wrap(raw)
	if _, err := e.Execute(ctx, modeltest.Reader(next), []core.Transaction{unfunded}); !errors.Is(err, core.ErrDeferredTransaction) {
		t.Fatal("insufficient funds accepted", err)
	}
	raw.Nonce = 3
	raw.Amount = 1
	raw.Sign(key)
	gap, _ := Wrap(raw)
	if _, err := e.Execute(ctx, modeltest.Reader(next), []core.Transaction{gap}); !errors.Is(err, core.ErrDeferredTransaction) {
		t.Fatal("nonce gap accepted", err)
	}
	other, _ := New(Config{Network: "other"})
	if err := other.ValidateTransaction(ctx, tx); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatal("cross network tx accepted")
	}
	raw.Amount = ^uint64(0)
	raw.Fee = 1
	if err := raw.Sign(key); err == nil {
		t.Fatal("overflow accepted")
	}
}
