package storage

import (
	"context"
	"errors"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"path/filepath"
	"sync"
	"testing"
)

func TestConfirmationAtomicDuringReorganization(t *testing.T) {
	for _, kind := range []string{"memory", "bolt"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			p := workFormat{testprotocol.New("confirmation")}
			var s interface {
				chaincap.BranchLedger
				query.ConfirmationReader
			}
			if kind == "memory" {
				s, _ = NewMemory(MemoryConfig{}, Deps{Protocol: p})
			} else {
				b, _ := NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, filepath.Join(t.TempDir(), "c.db")), Protocol: p})
				if err := b.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer b.Stop(ctx)
				s = b
			}
			shared := testprotocol.Tx{Label: "shared", Body: []byte{255, 0}}
			build := func(parent core.ID, txs []core.Transaction, receipt byte) (core.Block, core.ExecutionResult) {
				pb, err := s.Block(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				draft, err := p.Draft(ctx, pb, txs)
				if err != nil {
					t.Fatal(err)
				}
				snap, err := s.Snapshot(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				r, err := (testprotocol.Executor{P: p}).Execute(ctx, snap, draft)
				snap.Close()
				if err != nil {
					t.Fatal(err)
				}
				for _, tx := range txs {
					var data []byte
					if receipt != 0 {
						data = []byte{receipt}
					}
					r.Receipts = append(r.Receipts, core.Receipt{TransactionID: tx.ID(), Data: data})
				}
				b, err := p.Finalize(ctx, draft, r)
				if err != nil {
					t.Fatal(err)
				}
				r.BlockID = b.ID()
				return b, r
			}
			type expectation struct {
				block core.ID
				index uint64
				data  byte
			}
			var known sync.Map
			g, _ := s.Head(ctx)
			a, ar := build(g, []core.Transaction{shared}, 0)
			known.Store(a.ID(), expectation{a.ID(), 0, 0})
			if _, err := s.Import(ctx, g, a, ar); err != nil {
				t.Fatal(err)
			}
			b, br := build(g, []core.Transaction{testprotocol.Tx{Label: "prefix"}, shared}, 1)
			known.Store(b.ID(), expectation{b.ID(), 1, 1})
			if _, err := s.Import(ctx, g, b, br); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			finished := make(chan struct{})
			fail := make(chan error, 1)
			go func() {
				defer close(finished)
				for {
					select {
					case <-done:
						return
					default:
					}
					c, err := s.Confirmation(ctx, shared.ID())
					if err != nil {
						select {
						case fail <- err:
						default:
						}
						return
					}
					expected, ok := known.Load(c.Head)
					if !ok {
						select {
						case fail <- errors.New("unrecognized head"):
						default:
						}
						return
					}
					want := expected.(expectation)
					if c.Transaction.BlockID != want.block || c.Transaction.Index != want.index || c.Receipt == nil || c.Receipt.TransactionID != shared.ID() || want.data == 0 && len(c.Receipt.Data) != 0 || want.data != 0 && (len(c.Receipt.Data) != 1 || c.Receipt.Data[0] != want.data) {
						select {
						case fail <- errors.New("torn confirmation view"):
						default:
						}
						return
					}
				}
			}()
			tails := []core.ID{a.ID(), b.ID()}
			for n := 0; n < 30; n++ {
				for branch := 0; branch < 2; branch++ {
					parent := tails[branch]
					block, r := build(parent, nil, 0)
					want := expectation{a.ID(), 0, 0}
					if branch == 1 {
						want = expectation{b.ID(), 1, 1}
					}
					known.Store(block.ID(), want)
					if _, err := s.Import(ctx, parent, block, r); err != nil {
						t.Fatal(err)
					}
					tails[branch] = block.ID()
				}
			}
			close(done)
			<-finished
			select {
			case err := <-fail:
				t.Fatal(err)
			default:
			}
			c, err := s.Confirmation(ctx, shared.ID())
			if err != nil {
				t.Fatal(err)
			}
			if len(c.Receipt.Data) > 0 {
				c.Receipt.Data[0] ^= 255
			}
			tx := c.Transaction.Transaction.(testprotocol.Tx)
			tx.Body[0] = 0
			again, err := s.Confirmation(ctx, shared.ID())
			if err != nil || again.Transaction.Transaction.(testprotocol.Tx).Body[0] != 255 {
				t.Fatal("transaction aliases storage", err)
			}
			if _, err = s.Confirmation(ctx, "unknown"); err != core.ErrNotFound {
				t.Fatal(err)
			}
		})
	}
}
