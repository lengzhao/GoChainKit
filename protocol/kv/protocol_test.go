package kv

import (
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/protocol/kv/types"
	"testing"
)

func TestProtocolRoundTrip(t *testing.T) {
	p, e := New(Config{ChainID: "test"})
	if e != nil {
		t.Fatal(e)
	}
	tx := types.Transaction{ChainID: "test", Nonce: 1, Key: []byte("k"), Value: []byte("v")}
	if e = tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); e != nil {
		t.Fatal(e)
	}
	w, e := WrapTransaction(tx)
	if e != nil {
		t.Fatal(e)
	}
	tx.Value[0] = 'x'
	v, e := UnwrapTransaction(w)
	if e != nil || string(v.Value) != "v" {
		t.Fatal("mutable transaction")
	}
	data, e := p.EncodeTransaction(w)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := p.DecodeTransaction(data)
	if e != nil || copy.ID() != w.ID() {
		t.Fatal("roundtrip", e)
	}
	if _, e = p.DecodeTransaction(append(data, []byte(" {}")...)); e == nil {
		t.Fatal("trailing JSON")
	}
	g, _, e := p.Genesis(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	d, e := p.Draft(context.Background(), g, nil)
	_ = d
	_ = e
}
