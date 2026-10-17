package types

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"testing"
)

func TestTransactionSigning(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	tx := Transaction{ChainID: "dev", Nonce: 1, Key: []byte("key"), Value: []byte("value")}
	if err := tx.Sign(key); err != nil {
		t.Fatal(err)
	}
	if err := tx.Verify("dev"); err != nil {
		t.Fatal(err)
	}
	a, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	b, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("unstable encoding")
	}
	if err := tx.Verify("other"); err == nil {
		t.Fatal("accepted wrong chain")
	}
	tx.Nonce++
	if err := tx.Verify("dev"); err == nil {
		t.Fatal("accepted changed nonce")
	}
}

func TestRejectMalformedSigningInputs(t *testing.T) {
	tx := Transaction{ChainID: "dev", Nonce: 1, Key: []byte("key")}
	if err := tx.Sign(nil); err == nil {
		t.Fatal("accepted nil key")
	}
	if err := tx.Verify("dev"); err == nil {
		t.Fatal("accepted unsigned transaction")
	}
	tx.Value = make([]byte, MaxValueBytes+1)
	if _, err := tx.MarshalBinary(); err == nil {
		t.Fatal("accepted oversized value")
	}
}

func TestStateRootDeterministic(t *testing.T) {
	a := map[string][]byte{"b": []byte("2"), "a": []byte("1")}
	b := map[string][]byte{"a": []byte("1"), "b": []byte("2")}
	if got := fmt.Sprintf("%x", StateRoot(a)); got != "30e260cb2b011e0da0e09f531fec445146b6e7fa44cffa52e336b74a864d54df" {
		t.Fatalf("state encoding golden vector changed: %s", got)
	}
	if StateRoot(a) != StateRoot(b) {
		t.Fatal("map order changed root")
	}
	b["a"] = []byte("changed")
	if StateRoot(a) == StateRoot(b) {
		t.Fatal("value change did not change root")
	}
}
