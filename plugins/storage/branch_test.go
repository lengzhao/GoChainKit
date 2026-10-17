package storage

import (
	"context"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"math/big"
	"path/filepath"
	"testing"
)

type workFormat struct{ *testprotocol.Format }

func (p workFormat) Parent(b core.Block) (core.ID, error) {
	return b.(testprotocol.Frame).Ancestor, nil
}
func (p workFormat) Work(b core.Block) (*big.Int, error) {
	if b.(testprotocol.Frame).Revision == 0 {
		return big.NewInt(0), nil
	}
	return big.NewInt(1), nil
}
func (p workFormat) Seal(_ context.Context, b core.Block) (core.Block, error) { return b, nil }
func TestBranchReorg(t *testing.T) {
	for _, backend := range []string{"memory", "bolt"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			p := workFormat{testprotocol.New("branches")}
			var s chaincap.BranchLedger
			path := filepath.Join(t.TempDir(), "branches.db")
			if backend == "memory" {
				s, _ = NewMemory(MemoryConfig{}, Deps{Protocol: p})
			} else {
				v, _ := NewBolt(BoltConfig{path}, Deps{Protocol: p})
				if e := v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				defer func() { v.Stop(ctx) }()
				s = v
			}
			g, _ := s.Head(ctx)
			build := func(parent core.ID, label string) (core.Block, core.ExecutionResult) {
				pb, e := s.Block(ctx, parent)
				if e != nil {
					t.Fatal(e)
				}
				d, e := p.Draft(ctx, pb, []core.Transaction{testprotocol.Tx{Label: label, Body: []byte(label)}})
				if e != nil {
					t.Fatal(e)
				}
				snap, e := s.Snapshot(ctx, parent)
				if e != nil {
					t.Fatal(e)
				}
				r, e := (testprotocol.Executor{P: p}).Execute(ctx, snap, d)
				snap.Close()
				if e != nil {
					t.Fatal(e)
				}
				b, e := p.Finalize(ctx, d, r)
				if e != nil {
					t.Fatal(e)
				}
				r.BlockID = b.ID()
				return b, r
			}
			a, ar := build(g, "a")
			if _, e := s.Import(ctx, g, a, ar); e != nil {
				t.Fatal(e)
			}
			b, br := build(g, "b")
			change, e := s.Import(ctx, g, b, br)
			if e != nil {
				t.Fatal(e)
			}
			if change.NewHead != a.ID() {
				t.Fatal("equal work replaced head")
			}
			c, cr := build(b.ID(), "c")
			change, e = s.Import(ctx, b.ID(), c, cr)
			if e != nil {
				t.Fatal(e)
			}
			if len(change.Detached) != 1 || change.Detached[0].ID() != a.ID() || len(change.Attached) != 2 || change.Attached[0].ID() != b.ID() || change.NewHead != c.ID() {
				t.Fatal("wrong reorg", change)
			}
			if _, e = s.Transaction(ctx, testprotocol.Tx{Label: "a", Body: []byte("a")}.ID()); e == nil {
				t.Fatal("detached tx still canonical")
			}
			if _, e = s.Transaction(ctx, testprotocol.Tx{Label: "b", Body: []byte("b")}.ID()); e != nil {
				t.Fatal(e)
			}
			if backend == "bolt" {
				s.(*BoltStore).Stop(ctx)
				next, _ := NewBolt(BoltConfig{path}, Deps{Protocol: p})
				if e := next.Start(ctx); e != nil {
					t.Fatal(e)
				}
				defer next.Stop(ctx)
				head, _ := next.Head(ctx)
				if head != c.ID() {
					t.Fatal("reorg not durable")
				}
				if _, e := next.Block(ctx, a.ID()); e != nil {
					t.Fatal("side branch lost", e)
				}
			}
		})
	}
}
