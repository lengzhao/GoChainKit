package storage

import (
	"context"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"path/filepath"
	"testing"

	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	db "go.etcd.io/bbolt"
)

// An import must not enumerate unrelated side branches or recreate canonical
// index buckets. Recovery deliberately remains a complete integrity check.
func TestBranchImportTouchesOnlyRelevantRecords(t *testing.T) {
	ctx := context.Background()
	p, _ := pow.New(pow.Config{ChainID: "incremental"})
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, _ := NewBolt(BoltConfig{path}, Deps{Protocol: p})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer s.Stop(ctx)
	g, _ := s.Head(ctx)
	a, ar := powChild(t, p, s, g, 1, "main")
	if _, err := s.Import(ctx, g, a, ar); err != nil {
		t.Fatal(err)
	}
	side, sr := powChild(t, p, s, g, 1, "side")
	if _, err := s.Import(ctx, g, side, sr); err != nil {
		t.Fatal(err)
	}
	b, br := powChild(t, p, s, a.ID(), 2, "next")
	var saved []byte
	if err := s.db.Update(func(tx *db.Tx) error {
		bucket := tx.Bucket([]byte("branches"))
		saved = append([]byte(nil), bucket.Get([]byte(side.ID()))...)
		if err := bucket.Put([]byte(side.ID()), []byte("corrupt unrelated fork")); err != nil {
			return err
		}
		return tx.Bucket([]byte("transactions")).SetSequence(77)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, a.ID(), b, br); err != nil {
		t.Fatalf("import consulted unrelated fork: %v", err)
	}
	if err := s.db.View(func(tx *db.Tx) error {
		if got := tx.Bucket([]byte("transactions")).Sequence(); got != 77 {
			t.Errorf("canonical bucket recreated: sequence %d", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Update(func(tx *db.Tx) error { return tx.Bucket([]byte("branches")).Put([]byte(side.ID()), saved) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _ := NewBolt(BoltConfig{path}, Deps{Protocol: p})
	if err := reopened.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop(ctx)
	if got, err := reopened.Head(ctx); err != nil || got != b.ID() {
		t.Fatalf("reopen head %q %v", got, err)
	}
	if _, err := reopened.Transaction(ctx, core.ID("missing")); err != core.ErrNotFound {
		t.Fatal(err)
	}
}

// A transaction present on both sides must be reinserted with the attached
// block's position after the detached index is removed.
func TestBranchReorgReindexesSharedTransaction(t *testing.T) {
	for _, backend := range []string{"memory", "bolt"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			p := workFormat{testprotocol.New("shared-index")}
			var s chaincap.BranchLedger
			if backend == "memory" {
				s, _ = NewMemory(MemoryConfig{}, Deps{Protocol: p})
			} else {
				b, _ := NewBolt(BoltConfig{filepath.Join(t.TempDir(), "ledger.db")}, Deps{Protocol: p})
				if err := b.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer b.Stop(ctx)
				s = b
			}
			build := func(parent core.ID, labels ...string) (core.Block, core.ExecutionResult) {
				pb, err := s.Block(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				var txs []core.Transaction
				for _, label := range labels {
					txs = append(txs, testprotocol.Tx{Label: label, Body: []byte(label)})
				}
				d, err := p.Draft(ctx, pb, txs)
				if err != nil {
					t.Fatal(err)
				}
				snap, err := s.Snapshot(ctx, parent)
				if err != nil {
					t.Fatal(err)
				}
				r, err := (testprotocol.Executor{P: p}).Execute(ctx, snap, d)
				snap.Close()
				if err != nil {
					t.Fatal(err)
				}
				for i, tx := range txs {
					r.Receipts = append(r.Receipts, core.Receipt{TransactionID: tx.ID(), Data: []byte{byte(i)}})
				}
				b, err := p.Finalize(ctx, d, r)
				if err != nil {
					t.Fatal(err)
				}
				r.BlockID = b.ID()
				return b, r
			}
			g, _ := s.Head(ctx)
			a, ar := build(g, "shared")
			if _, err := s.Import(ctx, g, a, ar); err != nil {
				t.Fatal(err)
			}
			b, br := build(g, "other", "shared")
			if _, err := s.Import(ctx, g, b, br); err != nil {
				t.Fatal(err)
			}
			c, cr := build(b.ID(), "tip")
			if _, err := s.Import(ctx, b.ID(), c, cr); err != nil {
				t.Fatal(err)
			}
			tx := testprotocol.Tx{Label: "shared", Body: []byte("shared")}
			record, err := s.Transaction(ctx, tx.ID())
			if err != nil {
				t.Fatal(err)
			}
			if record.BlockID != b.ID() || record.Index != 1 {
				t.Fatalf("wrong attached transaction index: %+v", record)
			}
			receipt, err := s.Receipt(ctx, tx.ID())
			if err != nil {
				t.Fatal(err)
			}
			if receipt.TransactionID != tx.ID() || len(receipt.Data) != 1 || receipt.Data[0] != 1 {
				t.Fatalf("wrong attached receipt %+v", receipt)
			}
		})
	}
}
