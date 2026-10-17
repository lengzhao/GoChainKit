package storage

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	db "go.etcd.io/bbolt"
	"path/filepath"
	"testing"
)

func powChild(t *testing.T, p core.WorkProtocol, s core.BranchLedger, parent core.ID, nonce uint64, value string) (core.Block, core.ExecutionResult) {
	t.Helper()
	ctx := context.Background()
	tx := pow.Transaction{ChainID: p.Protocol().Network, Nonce: nonce, Key: []byte("k"), Value: []byte(value)}
	if e := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); e != nil {
		t.Fatal(e)
	}
	w, e := pow.WrapTransaction(tx)
	if e != nil {
		t.Fatal(e)
	}
	pb, e := s.Block(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	d, e := p.Draft(ctx, pb, []core.Transaction{w})
	if e != nil {
		t.Fatal(e)
	}
	snap, e := s.Snapshot(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	ex, e := pow.NewExecutor(p)
	if e != nil {
		t.Fatal(e)
	}
	r, e := ex.Execute(ctx, snap, d)
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
func TestPoWBranchStateAndDurability(t *testing.T) {
	ctx := context.Background()
	p, _ := pow.New(pow.Config{ChainID: "pow-dev"})
	for _, kind := range []string{"memory", "bolt"} {
		t.Run(kind, func(t *testing.T) {
			var s core.BranchLedger
			path := filepath.Join(t.TempDir(), "pow.db")
			if kind == "memory" {
				v, e := NewMemory(MemoryConfig{}, Deps{p})
				if e != nil {
					t.Fatal(e)
				}
				s = v
			} else {
				v, e := NewBolt(BoltConfig{path}, Deps{p})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				defer v.Stop(ctx)
				s = v
			}
			g, _ := s.Head(ctx)
			a, ar := powChild(t, p, s, g, 1, "a")
			if _, e := s.Import(ctx, g, a, ar); e != nil {
				t.Fatal(e)
			}
			b, br := powChild(t, p, s, g, 1, "b")
			if _, e := s.Import(ctx, g, b, br); e != nil {
				t.Fatal(e)
			}
			c, cr := powChild(t, p, s, b.ID(), 2, "c")
			if _, e := s.Import(ctx, b.ID(), c, cr); e != nil {
				t.Fatal(e)
			}
			head, _ := s.Head(ctx)
			if head != c.ID() {
				t.Fatal("not reorged")
			}
			snap, _ := s.Snapshot(ctx, head)
			if snap.Root() != cr.StateRoot {
				t.Fatal("incorrect canonical state")
			}
			snap.Close()
			if kind == "bolt" {
				s.(*BoltStore).Stop(ctx)
				v, _ := NewBolt(BoltConfig{path}, Deps{p})
				if e := v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				defer v.Stop(ctx)
				if h, _ := v.Head(ctx); h != c.ID() {
					t.Fatal("head not recovered")
				}
			}
		})
	}
}
func TestBranchRollbackAndCorruptSideRecovery(t *testing.T) {
	ctx := context.Background()
	p, _ := pow.New(pow.Config{ChainID: "rollback"})
	path := filepath.Join(t.TempDir(), "pow.db")
	s, _ := NewBolt(BoltConfig{path}, Deps{p})
	if e := s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	g, _ := s.Head(ctx)
	a, ar := powChild(t, p, s, g, 1, "a")
	s.Import(ctx, g, a, ar)
	b, br := powChild(t, p, s, g, 1, "b")
	failure := errors.New("abort branch")
	s.beforeHead = func() error { return failure }
	if _, e := s.Import(ctx, g, b, br); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	if _, e := s.Block(ctx, b.ID()); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("partial branch")
	}
	s.beforeHead = nil
	if _, e := s.Import(ctx, g, b, br); e != nil {
		t.Fatal(e)
	}
	if e := s.db.Update(func(tx *db.Tx) error {
		var n branchNode
		if e := get(tx, "branches", []byte(b.ID()), &n); e != nil {
			return e
		}
		n.Total = "999"
		return put(tx, "branches", []byte(b.ID()), n)
	}); e != nil {
		t.Fatal(e)
	}
	s.Stop(ctx)
	next, _ := NewBolt(BoltConfig{path}, Deps{p})
	if e := next.Start(ctx); e == nil {
		next.Stop(ctx)
		t.Fatal("accepted corrupt side branch")
	}
}
