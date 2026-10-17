package kv

import (
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/core"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/GoChainKit/protocol/kv/types"
	"testing"
)

type snapshot struct{}

func (snapshot) Get(context.Context, []byte) ([]byte, error)        { return nil, core.ErrNotFound }
func (snapshot) Entries(context.Context) (map[string][]byte, error) { return map[string][]byte{}, nil }
func (snapshot) Root() core.Commitment                              { return "" }
func (snapshot) Close() error                                       { return nil }
func TestExecuteFinalize(t *testing.T) {
	ctx := context.Background()
	p, _ := protocol.New(protocol.Config{ChainID: "test"})
	e, err := New(Config{ChainID: "test"}, Deps{Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := p.Genesis(ctx)
	tx := types.Transaction{ChainID: "test", Nonce: 1, Key: []byte("k"), Value: []byte("v")}
	tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32)))
	w, _ := protocol.WrapTransaction(tx)
	d, err := p.Draft(ctx, g, []core.Transaction{w})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.Execute(ctx, snapshot{}, d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Finalize(ctx, d, r)
	if err != nil {
		t.Fatal(err)
	}
	r.BlockID = b.ID()
	if err = p.ValidateBlock(ctx, nil, g, b, r); err != nil {
		t.Fatal(err)
	}
	data, err := p.EncodeBlock(b)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := p.DecodeBlock(data)
	if err != nil || copy.ID() != b.ID() {
		t.Fatal("block roundtrip", err)
	}
	r.Receipts[0].Data[0] = 0
	if err = p.ValidateBlock(ctx, nil, g, b, r); err == nil {
		t.Fatal("invalid receipt accepted")
	}
	bad, _ := p.Draft(ctx, g, []core.Transaction{w, w})
	if _, err = e.Execute(ctx, snapshot{}, bad); err == nil {
		t.Fatal("duplicate nonce accepted")
	}
}
