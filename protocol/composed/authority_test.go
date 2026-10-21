package composed_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	choice "github.com/lengzhao/GoChainKit/choice/authority"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/poa"
	seal "github.com/lengzhao/GoChainKit/seal/authority"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"github.com/lengzhao/GoChainKit/validator/static"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorityAccountUTXOReorgRecovery(t *testing.T) {
	for _, kind := range []string{"account", "utxo"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
			owner := asset.AddressOf(key[32:])
			recipient := assetcap.Address{2}
			producer := []byte("authority-a")
			var engine model.Engine
			var tx core.Transaction
			var input utxo.Outpoint
			if kind == "account" {
				m, e := account.New(account.Config{Network: "mix", Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
				if e != nil {
					t.Fatal(e)
				}
				raw := account.Transaction{Network: "mix", Nonce: 1, To: recipient, Amount: 7, Fee: 0}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = account.Wrap(raw)
				if e != nil {
					t.Fatal(e)
				}
				engine = m
			} else {
				m, e := utxo.New(utxo.Config{Network: "mix", Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
				if e != nil {
					t.Fatal(e)
				}
				input = m.GenesisOutpoints()[0]
				raw := utxo.Transaction{Network: "mix", Inputs: []utxo.Input{{Previous: input}}, Outputs: []utxo.Output{{To: recipient, Amount: 7}, {To: owner, Amount: 93}}}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = utxo.Wrap(raw)
				if e != nil {
					t.Fatal(e)
				}
				engine = m
			}
			now := int64(1000000010)
			clock := func() time.Time { return time.Unix(now, 0) }
			consensusKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, 32))
			members, _ := static.New(static.Config{Members: []valcap.Member{{ID: core.ID(string(producer)), Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: consensusKey[32:]}}}})
			rule, e := poa.New(poa.Config{GenesisTime: 1000000000, SlotSeconds: 1, EpochSlots: 10}, poa.Deps{Members: members, Verifier: &suite.Verifier{}})
			if e != nil {
				t.Fatal(e)
			}
			p, e := composed.New(composed.Config{Network: "mix", GenesisTime: 1000000000, Producer: producer, Clock: clock}, engine, rule, &choice.Choice{})
			if e != nil {
				t.Fatal(e)
			}
			keys := keystore.NewMemory()
			if e = keys.Create(ctx, "consensus", consensusKey); e != nil {
				t.Fatal(e)
			}
			signer, _ := custody.New(custody.Config{KeyID: "consensus"}, custody.Deps{Store: keys})
			sealer, _ := seal.New(seal.Config{Clock: clock}, seal.Deps{Rule: rule, Signer: signer})
			path := filepath.Join(t.TempDir(), "chain.db")
			s, e := storage.NewKV(storage.KVConfig{}, storage.KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer func() { s.Stop(ctx) }()
			executor, e := composed.NewExecutor(p, s)
			if e != nil {
				t.Fatal(e)
			}
			build := func(parent core.ID, txs []core.Transaction) core.Block {
				now++
				pb, e := s.Block(ctx, parent)
				if e != nil {
					t.Fatal(e)
				}
				draft, e := p.Draft(ctx, pb, txs)
				if e != nil {
					t.Fatal(e)
				}
				snap, _ := s.Snapshot(ctx, parent)
				r, e := executor.Execute(ctx, snap, draft)
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
				b, e = sealer.Seal(ctx, pb, b)
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
			s, e = storage.NewKV(storage.KVConfig{}, storage.KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
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

// Invalid remote scheduling fields must be rejected before contextual execution,
// where Draft reconstructs consensus metadata from that same header.
func TestAuthorityInvalidRemoteCandidateBeforeExecution(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
	members, _ := static.New(static.Config{Members: []valcap.Member{{ID: "a", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: key[32:]}}}})
	rule, _ := poa.New(poa.Config{GenesisTime: 100, SlotSeconds: 2, EpochSlots: 10}, poa.Deps{Members: members, Verifier: &suite.Verifier{}})
	m, _ := account.New(account.Config{Network: "candidate"})
	p, e := composed.New(composed.Config{Network: "candidate", GenesisTime: 100, Producer: []byte("a"), Clock: func() time.Time { return time.Unix(102, 0) }}, m, rule, &choice.Choice{})
	if e != nil {
		t.Fatal(e)
	}
	g, _, _ := p.Genesis(ctx)
	draft, e := p.Draft(ctx, g, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.ValidateCandidate(ctx, nil, g, draft); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*composed.Envelope){func(v *composed.Envelope) { v.Header.Timestamp = 100 }, func(v *composed.Envelope) { v.Header.Producer = []byte("b") }, func(v *composed.Envelope) { v.Header.Consensus[0]++ }} {
		v, _ := composed.Unwrap(draft)
		change(&v)
		b, _ := composed.Wrap(v)
		if e = p.ValidateCandidate(ctx, nil, g, b); e == nil {
			t.Fatal("invalid remote schedule accepted")
		}
	}
}
