package wallet

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	utxoapi "github.com/lengzhao/GoChainKit/api/http/utxo"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildTransferSelectsMatureCoinsChangeAndFee(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(key[32:])
	to := assetcap.Address{2}
	coins := []utxoapi.Coin{{TransactionID: hex.EncodeToString(make([]byte, 32)), Amount: 100, Spendable: false}, {TransactionID: hex.EncodeToString(append(make([]byte, 31), 1)), Amount: 30, Spendable: true}, {TransactionID: hex.EncodeToString(append(make([]byte, 31), 2)), Amount: 40, Spendable: true}}
	tx, err := BuildTransfer("dev", key, to, 50, 3, coins)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := utxo.Unwrap(tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Inputs) != 2 || len(raw.Outputs) != 2 || raw.Outputs[0].Amount != 50 || raw.Outputs[1].To != owner || raw.Outputs[1].Amount != 17 {
		t.Fatal(raw)
	}
	for _, amount := range []uint64{0, 100, ^uint64(0)} {
		if _, err = BuildTransfer("dev", key, to, amount, 3, coins); err == nil {
			t.Fatal("invalid amount accepted", amount)
		}
	}
	coins[1].TransactionID = "bad"
	if _, err = BuildTransfer("dev", key, to, 1, 0, coins); err == nil {
		t.Fatal("bad outpoint accepted")
	}
}
func TestWalletKeysExclusiveAndValidated(t *testing.T) {
	ctx := context.Background()
	store := keystore.NewMemory()
	address, err := Create(ctx, store, "wallet")
	if err != nil {
		t.Fatal(err)
	}
	key, err := Load(ctx, store, "wallet")
	if err != nil || address != asset.AddressOf(key[32:]) {
		t.Fatal(address, err)
	}
	if _, err = Create(ctx, store, "wallet"); err == nil {
		t.Fatal("overwrote wallet")
	}
	if err = store.Create(ctx, "bad", []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, store, "bad"); err == nil {
		t.Fatal("malformed key accepted")
	}
}

func TestSignEncodedTransactionForOfflineSubmission(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	raw := utxo.Transaction{Network: "offline", Inputs: []utxo.Input{{Previous: utxo.Outpoint{TransactionID: [32]byte{1}}}}, Outputs: []utxo.Output{{To: assetcap.Address{2}, Amount: 3}}}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignEncoded(key, data)
	if err != nil {
		t.Fatal(err)
	}
	engine, _ := utxo.New(utxo.Config{Network: "offline"})
	decoded, err := engine.DecodeTransaction(signed.Data)
	if err != nil || signed.ID != hex.EncodeToString([]byte(decoded.ID())) {
		t.Fatal(signed, err)
	}
	if _, err = SignEncoded(key, []byte(`{"Network":"offline","unknown":1}`)); err == nil {
		t.Fatal("unknown fields accepted")
	}
	if _, err = SignEncoded(key, make([]byte, (1<<20)+1)); err == nil {
		t.Fatal("oversized input accepted")
	}
}
func TestClientAnchorsPaginationAndRejectsWrongOwner(t *testing.T) {
	owner := assetcap.Address{1}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 && r.URL.Query().Get("at") != "abcd" {
			t.Error("unanchored second page")
		}
		b := utxoapi.Balance{At: "abcd", Address: hex.EncodeToString(owner[:]), Height: 3, Coins: []utxoapi.Coin{{Amount: 1}}}
		if calls == 1 {
			b.NextOffset = 1
		}
		json.NewEncoder(w).Encode(b)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.Balance(context.Background(), owner)
	if err != nil || len(b.Coins) != 2 {
		t.Fatal(b, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(utxoapi.Balance{Address: "wrong"})
	}))
	defer bad.Close()
	client, _ = NewClient(bad.URL)
	if _, err = client.Balance(context.Background(), owner); err == nil {
		t.Fatal("wrong owner response accepted")
	}
}
