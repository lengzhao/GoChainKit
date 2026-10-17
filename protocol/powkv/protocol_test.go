package powkv

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
)

func TestSealedRoundTripAndCommitments(t *testing.T) {
	ctx := context.Background()
	p, err := New(Config{ChainID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	g, s, err := p.Genesis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w, e := p.Work(g); e != nil || w.Sign() != 0 {
		t.Fatal(w, e)
	}
	d, err := p.Draft(ctx, g, nil)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := p.StateCommitment(ctx, s)
	r := core.ExecutionResult{Parent: g.ID(), BlockID: d.ID(), StateRoot: root}
	b, err := p.Finalize(ctx, d, r)
	if err != nil {
		t.Fatal(err)
	}
	w, err := p.Work(b)
	if err != nil || w.Int64() != 256 {
		t.Fatal(w, err)
	}
	parent, err := p.Parent(b)
	if err != nil || parent != g.ID() {
		t.Fatal("parent", err)
	}
	data, err := p.EncodeBlock(b)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := p.DecodeBlock(data)
	if err != nil || restored.ID() != b.ID() {
		t.Fatal("codec", err)
	}
	r.BlockID = b.ID()
	if err = p.ValidateBlock(ctx, nil, g, b, r); err != nil {
		t.Fatal(err)
	}
	r.StateRoot = core.Commitment(string(make([]byte, 32)))
	if err = p.ValidateBlock(ctx, nil, g, b, r); err == nil {
		t.Fatal("accepted incorrect state root")
	}
	v, _ := UnwrapBlock(b)
	for {
		v.Nonce++
		bad, _ := WrapBlock(v)
		if _, e := p.Work(bad); e != nil {
			break
		}
	}
	bad, _ := WrapBlock(v)
	r.BlockID = bad.ID()
	if err = p.ValidateBlock(ctx, nil, g, bad, r); err == nil {
		t.Fatal("accepted bad proof")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = p.Seal(cancelCtx, d); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestGenesisHasNoParent(t *testing.T) {
	p, _ := New(Config{ChainID: "genesis"})
	g, _, _ := p.Genesis(context.Background())
	parent, err := p.Parent(g)
	if err != nil || parent != "" {
		t.Fatalf("genesis parent %x: %v", parent, err)
	}
	v, _ := UnwrapBlock(g)
	v.Nonce++
	forged, err := WrapBlock(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Parent(forged); err == nil {
		t.Fatal("accepted forged genesis")
	}
}
