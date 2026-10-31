package composed_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"testing"
)

func TestReferenceBodyStableWireAndFailClosed(t *testing.T) {
	ctx := context.Background()
	m, _ := account.New(account.Config{Network: "ref"})
	rule, _ := pow.New(pow.Config{})
	p, e := composed.NewPoW(composed.Config{Network: "ref", GenesisTime: 1700000000, ReferenceBody: true}, m, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	full, _ := composed.NewPoW(composed.Config{Network: "ref", GenesisTime: 1700000000}, m, rule, rule)
	if full.Protocol() == p.Protocol() {
		t.Fatal("mode not bound")
	}
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	raw := account.Transaction{Network: "ref", Nonce: 1, Amount: 1}
	raw.Sign(key)
	tx, _ := account.Wrap(raw)
	g, _, _ := p.Genesis(ctx)
	b, e := p.Draft(ctx, g, []core.Transaction{tx})
	if e != nil {
		t.Fatal(e)
	}
	wire, e := p.EncodeBlock(b)
	if e != nil {
		t.Fatal(e)
	}
	cold, e := p.DecodeBlock(wire)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Transactions(cold); e == nil {
		t.Fatal("missing body executed")
	}
	warm, e := p.Attach(cold, []core.Transaction{tx})
	if e != nil {
		t.Fatal(e)
	}
	wire2, _ := p.EncodeBlock(warm)
	if !bytes.Equal(wire, wire2) || warm.ID() != cold.ID() {
		t.Fatal("hydration changed wire or identity")
	}
	if _, e = p.Attach(cold, nil); e == nil {
		t.Fatal("missing accepted")
	}
	refs, _ := p.References(cold)
	if len(refs) != 1 || refs[0] != tx.ID() {
		t.Fatal(refs)
	}
	env, _ := composed.Unwrap(cold)
	env.Header.TransactionsRoot[0] ^= 1
	bad, _ := composed.Wrap(env)
	if _, e = p.Attach(bad, []core.Transaction{tx}); e == nil {
		t.Fatal("bad commitment accepted")
	}
}

func TestReferenceWireRejectsEmptyAttachedArray(t *testing.T) {
	m, _ := account.New(account.Config{Network: "wire"})
	rule, _ := pow.New(pow.Config{})
	p, _ := composed.NewPoW(composed.Config{Network: "wire", GenesisTime: 1700000000, ReferenceBody: true}, m, rule, rule)
	g, _, _ := p.Genesis(context.Background())
	raw, _ := p.EncodeBlock(g)
	raw = bytes.Replace(raw, []byte(`"Transactions":null`), []byte(`"Transactions":[]`), 1)
	if _, e := p.DecodeBlock(raw); e == nil {
		t.Fatal("noncanonical auxiliary array accepted")
	}
}
