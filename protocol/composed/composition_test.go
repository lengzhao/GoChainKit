package composed_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"path/filepath"
	"testing"
	"time"
)

func TestPoWAccountUTXOReorgRecovery(t *testing.T) {
	for _, kind := range []string{"account", "utxo"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
			owner := asset.AddressOf(key[32:])
			recipient := asset.Address{2}
			miner := asset.Address{3}
			var engine model.Engine
			var tx core.Transaction
			var input utxo.Outpoint
			if kind == "account" {
				m, e := account.New(account.Config{Network: "mix", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
				if e != nil {
					t.Fatal(e)
				}
				raw := account.Transaction{Network: "mix", Nonce: 1, To: recipient, Amount: 7, Fee: 3}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = account.Wrap(raw)
				if e != nil {
					t.Fatal(e)
				}
				engine = m
			} else {
				m, e := utxo.New(utxo.Config{Network: "mix", Allocations: []asset.Allocation{{Address: owner, Amount: 100}}})
				if e != nil {
					t.Fatal(e)
				}
				input = m.GenesisOutpoints()[0]
				raw := utxo.Transaction{Network: "mix", Inputs: []utxo.Input{{Previous: input}}, Outputs: []utxo.Output{{To: recipient, Amount: 7}, {To: owner, Amount: 90}}}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = utxo.Wrap(raw)
				if e != nil {
					t.Fatal(e)
				}
				engine = m
			}
			clock := func() time.Time { return time.Unix(1000000010, 0) }
			rule, e := pow.New(pow.Config{RetargetInterval: 100, TargetSpacing: 2, InitialReward: 50, Clock: clock})
			if e != nil {
				t.Fatal(e)
			}
			p, e := composed.NewPoW(composed.Config{Network: "mix", GenesisTime: 1000000000, Producer: miner[:], Clock: clock}, engine, rule, rule)
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(t.TempDir(), "chain.db")
			s, e := storage.NewBolt(storage.BoltConfig{Path: path}, storage.Deps{Protocol: p})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() { s.Stop(ctx) }()
			build := func(parent core.ID, txs []core.Transaction) core.Block {
				pb, e := s.Block(ctx, parent)
				if e != nil {
					t.Fatal(e)
				}
				draft, e := p.Draft(ctx, pb, txs)
				if e != nil {
					t.Fatal(e)
				}
				snap, _ := s.Snapshot(ctx, parent)
				r, e := p.Execute(ctx, snap, draft)
				snap.Close()
				if e != nil {
					t.Fatal(e)
				}
				b, e := p.Finalize(ctx, draft, r)
				if e != nil {
					t.Fatal(e)
				}
				r.BlockID = b.ID()
				if e = p.ValidateProposal(ctx, s, pb, b, r); e != nil {
					t.Fatal(e)
				}
				b, e = p.Seal(ctx, b)
				if e != nil {
					t.Fatal(e)
				}
				r.BlockID = b.ID()
				if e = p.ValidateBlock(ctx, s, pb, b, r); e != nil {
					t.Fatal(e)
				}
				if _, e = s.Import(ctx, parent, b, r); e != nil {
					t.Fatal(e)
				}
				return b
			}
			g, _ := s.Head(ctx)
			a := build(g, []core.Transaction{tx})
			snap, _ := s.Snapshot(ctx, a.ID())
			if kind == "account" {
				if v, e := account.Balance(ctx, snap, recipient); e != nil || v != 7 {
					t.Fatal("transfer failed", v, e)
				}
				if v, e := account.Balance(ctx, snap, miner); e != nil || v != 53 {
					t.Fatal("fees/reward mismatch", v, e)
				}
			} else {
				if _, e := utxo.Lookup(ctx, snap, input); !errors.Is(e, core.ErrNotFound) {
					t.Fatal("spent output persists", e)
				}
			}
			snap.Close()
			b := build(g, nil)
			c := build(b.ID(), nil)
			head, _ := s.Head(ctx)
			if head != c.ID() {
				t.Fatal("side chain did not win")
			}
			if _, e = s.Transaction(ctx, tx.ID()); !errors.Is(e, core.ErrNotFound) {
				t.Fatal("detached tx canonical", e)
			}
			snap, _ = s.Snapshot(ctx, c.ID())
			if kind == "account" {
				if v, e := account.Balance(ctx, snap, owner); e != nil || v != 100 {
					t.Fatal("reorg did not restore balance", v, e)
				}
			} else {
				if _, e := utxo.Lookup(ctx, snap, input); e != nil {
					t.Fatal("reorg did not restore output", e)
				}
			}
			snap.Close()
			d := build(c.ID(), []core.Transaction{tx})
			if e = s.Stop(ctx); e != nil {
				t.Fatal(e)
			}
			s, e = storage.NewBolt(storage.BoltConfig{Path: path}, storage.Deps{Protocol: p})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			head, _ = s.Head(ctx)
			if head != d.ID() {
				t.Fatal("head lost on reopen")
			}
			if _, e = s.Transaction(ctx, tx.ID()); e != nil {
				t.Fatal(e)
			}
			_ = build(head, nil)
		})
	}
}
