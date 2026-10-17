package storage

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	db "go.etcd.io/bbolt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcessExitDurability(t *testing.T) {
	if path := os.Getenv("GOCHAINKIT_STORAGE_TEST_PATH"); path != "" {
		s := openStore(t, path)
		parent, b, r := candidate(t, s)
		if os.Getenv("GOCHAINKIT_STORAGE_TEST_PHASE") == "before" {
			s.beforeHead = func() error { os.Exit(0); return nil }
		}
		if err := s.Commit(context.Background(), parent, b, r); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // Deliberately skip Stop and every deferred cleanup.
	}
	for _, phase := range []string{"before", "after"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chain.db")
			command := exec.Command(os.Args[0], "-test.run=^TestProcessExitDurability$", "-test.count=1")
			command.Env = append(os.Environ(), "GOCHAINKIT_STORAGE_TEST_PATH="+path, "GOCHAINKIT_STORAGE_TEST_PHASE="+phase)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("subprocess: %v: %s", err, output)
			}
			s := openStore(t, path)
			defer s.Stop(context.Background())
			head, err := s.Head(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Block(context.Background(), head)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := protocol.UnwrapBlock(b)
			expected := uint64(0)
			if phase == "after" {
				expected = 1
			}
			if raw.Header.Height != expected {
				t.Fatalf("height %d, want %d", raw.Header.Height, expected)
			}
			if expected == 1 {
				txs, _ := s.p.Transactions(b)
				txid := txs[0].ID()
				if _, err := s.Transaction(context.Background(), txid); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Receipt(context.Background(), txid); err != nil {
					t.Fatal(err)
				}
				snapshot, err := s.Snapshot(context.Background(), head)
				if err != nil {
					t.Fatal(err)
				}
				value, err := snapshot.Get(context.Background(), []byte{0xff, 0, 0x80})
				_ = snapshot.Close()
				if err != nil || !bytes.Equal(value, []byte{0xff, 0, 0x80}) {
					t.Fatal("state not durable after process exit", err)
				}
			}
		})
	}
}

func candidate(t *testing.T, s *BoltStore) (core.ID, core.Block, core.ExecutionResult) {
	t.Helper()
	ctx := context.Background()
	parent, err := s.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := s.Block(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	tx := protocol.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("k"), Value: []byte{0xff, 0, 0x80}}
	if err = tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	w, _ := protocol.WrapTransaction(tx)
	draft, err := mustProtocol("dev").Draft(ctx, previous, []core.Transaction{w})
	if err != nil {
		t.Fatal(err)
	}
	state := map[string][]byte{string([]byte{0xff, 0, 0x80}): tx.Value}
	root, _ := s.p.StateCommitment(ctx, state)
	r := core.ExecutionResult{Parent: parent, BlockID: draft.ID(), Writes: state, StateRoot: root, Receipts: []core.Receipt{{TransactionID: w.ID(), Data: []byte{1}}}}
	b, err := mustProtocol("dev").Finalize(ctx, draft, r)
	if err != nil {
		t.Fatal(err)
	}
	r.BlockID = b.ID()
	return parent, b, r
}
func openStore(t *testing.T, path string) *BoltStore {
	t.Helper()
	s, err := NewBolt(BoltConfig{Path: path}, Deps{Protocol: mustProtocol("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestDiskRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	parent, b, result := candidate(t, s)
	if err := s.Commit(ctx, parent, b, result); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	defer s.Stop(ctx)
	head, err := s.Head(ctx)
	if err != nil || head != result.BlockID {
		t.Fatal("head not recovered", err)
	}
	stored, err := s.Block(ctx, head)
	if err != nil || stored == nil {
		t.Fatal("block missing", err)
	}
	txs, _ := s.p.Transactions(b)
	txid := txs[0].ID()
	tx, err := s.Transaction(ctx, txid)
	if err != nil || tx.BlockID != head || tx.Transaction == nil {
		t.Fatal("transaction missing", err)
	}
	receipt, err := s.Receipt(ctx, txid)
	if err != nil || !bytes.Equal(receipt.Data, []byte{1}) {
		t.Fatal("receipt missing", err)
	}
	snapshot, err := s.Snapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	data, err := snapshot.Get(ctx, []byte{0xff, 0, 0x80})
	if err != nil || string(data) != string([]byte{0xff, 0, 0x80}) {
		t.Fatal("binary state damaged", err)
	}
	if err := s.Commit(ctx, parent, b, result); !errors.Is(err, core.ErrAlreadyCommitted) {
		t.Fatal("duplicate commit", err)
	}
}
func TestRollbackAndWrongChain(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	parent, b, result := candidate(t, s)
	failure := errors.New("injected failure")
	s.beforeHead = func() error { return failure }
	if err := s.Commit(ctx, parent, b, result); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	head, _ := s.Head(ctx)
	if head != parent {
		t.Fatal("partial head")
	}
	if _, err := s.Block(ctx, result.BlockID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("partial block", err)
	}
	txs, _ := s.p.Transactions(b)
	txid := txs[0].ID()
	if _, err := s.Transaction(ctx, txid); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("partial transaction", err)
	}
	if _, err := s.Receipt(ctx, txid); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("partial receipt", err)
	}
	if _, err := s.Snapshot(ctx, result.BlockID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("partial state", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, path)
	head, _ = s.Head(ctx)
	if head != parent {
		t.Fatal("rollback not durable")
	}
	_ = s.Stop(ctx)
	wrong, err := NewBolt(BoltConfig{Path: path}, Deps{Protocol: mustProtocol("other")})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.Start(ctx); err == nil {
		_ = wrong.Stop(ctx)
		t.Fatal("accepted different chain")
	}
}

func mustProtocol(network string) core.Protocol {
	p, e := protocol.New(protocol.Config{ChainID: network})
	if e != nil {
		panic(e)
	}
	return p
}

func TestOldFormatRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	if err := s.db.Update(func(tx *db.Tx) error { return tx.Bucket([]byte("meta")).Put([]byte("format"), []byte("1")) }); err != nil {
		t.Fatal(err)
	}
	s.Stop(context.Background())
	next, _ := NewBolt(BoltConfig{Path: path}, Deps{mustProtocol("dev")})
	if err := next.Start(context.Background()); err == nil {
		next.Stop(context.Background())
		t.Fatal("accepted old format")
	}
}

func TestBoltLedgerIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	owner := s.LedgerID()
	if owner == nil {
		t.Fatal("missing ledger owner")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := openStore(t, path)
	defer next.Stop(context.Background())
	if next.LedgerID() == nil || next.LedgerID() == owner {
		t.Fatal("new backend must have distinct owner even for same database")
	}
}
