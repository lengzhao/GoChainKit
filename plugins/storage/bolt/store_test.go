package bolt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
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
			expected := uint64(0)
			if phase == "after" {
				expected = 1
			}
			if b.Header.Height != expected {
				t.Fatalf("height %d, want %d", b.Header.Height, expected)
			}
			if expected == 1 {
				txid, _ := b.Transactions[0].ID()
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

func candidate(t *testing.T, s *Store) (types.Hash, types.Block, types.ExecutionResult) {
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
	tx := types.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("k"), Value: []byte{0xff, 0, 0x80}}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	id, _ := tx.ID()
	root, _ := types.TransactionRoot([]types.Transaction{tx})
	state := map[string][]byte{string([]byte{0xff, 0, 0x80}): tx.Value}
	receipts := []types.Receipt{{TransactionID: id, Success: true}}
	b := types.Block{Header: types.BlockHeader{Version: 1, ChainID: "dev", Parent: parent, Height: previous.Header.Height + 1, TransactionRoot: root, StateRoot: types.StateRoot(state), ReceiptRoot: types.ReceiptRoot(receipts)}, Transactions: []types.Transaction{tx}}
	bid, err := b.ID()
	if err != nil {
		t.Fatal(err)
	}
	return parent, b, types.ExecutionResult{Parent: parent, BlockID: bid, Writes: state, Receipts: receipts, StateRoot: b.Header.StateRoot}
}
func openStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := New(Config{ChainID: "dev", Path: path})
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
	if err != nil || len(stored.Transactions) != 1 {
		t.Fatal("block missing", err)
	}
	txid, _ := b.Transactions[0].ID()
	tx, err := s.Transaction(ctx, txid)
	if err != nil || tx.BlockID != head || tx.Transaction.Nonce != 1 {
		t.Fatal("transaction missing", err)
	}
	receipt, err := s.Receipt(ctx, txid)
	if err != nil || !receipt.Success {
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
	if err := s.Commit(ctx, parent, b, result); !errors.Is(err, contracts.ErrAlreadyCommitted) {
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
	if _, err := s.Block(ctx, result.BlockID); !errors.Is(err, contracts.ErrNotFound) {
		t.Fatal("partial block", err)
	}
	txid, _ := b.Transactions[0].ID()
	if _, err := s.Transaction(ctx, txid); !errors.Is(err, contracts.ErrNotFound) {
		t.Fatal("partial transaction", err)
	}
	if _, err := s.Receipt(ctx, txid); !errors.Is(err, contracts.ErrNotFound) {
		t.Fatal("partial receipt", err)
	}
	if _, err := s.Snapshot(ctx, result.BlockID); !errors.Is(err, contracts.ErrNotFound) {
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
	wrong, err := New(Config{ChainID: "other", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := wrong.Start(ctx); err == nil {
		_ = wrong.Stop(ctx)
		t.Fatal("accepted different chain")
	}
}
