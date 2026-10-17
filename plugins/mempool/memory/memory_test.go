package memory

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"sync"
	"testing"
)

func transaction(t *testing.T, n uint64) types.Transaction {
	t.Helper()
	tx := types.Transaction{ChainID: "dev", Nonce: n, Key: []byte("k"), Value: []byte("v")}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	return tx
}
func TestCapacitySelectionAndOwnership(t *testing.T) {
	ctx := context.Background()
	p, err := New(Config{ChainID: "dev", MaxTransactions: 1, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	tx := transaction(t, 1)
	original := tx.Clone()
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx.Value[0] = 'x'
	if err := p.Add(ctx, transaction(t, 2)); !errors.Is(err, contracts.ErrFull) {
		t.Fatalf("expected full, got %v", err)
	}
	small, err := p.Select(ctx, 1)
	if err != nil || len(small) != 0 {
		t.Fatal("byte budget ignored", err)
	}
	got, err := p.Select(ctx, 4096)
	if err != nil || len(got) != 1 {
		t.Fatal("selection failed", err)
	}
	if string(got[0].Value) != "v" {
		t.Fatal("retained caller memory")
	}
	got[0].Value[0] = 'x'
	got, err = p.Select(ctx, 4096)
	if err != nil || string(got[0].Value) != "v" {
		t.Fatal("returned internal memory")
	}
	id, err := original.ID()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := p.Remove(ctx, []types.Hash{id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Add(ctx, transaction(t, 2)); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidAndCanceled(t *testing.T) {
	p, err := New(Config{ChainID: "dev", MaxTransactions: 10, MaxBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	tx := transaction(t, 1)
	tx.Nonce++
	if err := p.Add(context.Background(), tx); !errors.Is(err, contracts.ErrInvalidTransaction) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Add(ctx, transaction(t, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("accepted unbounded configuration")
	}
}
func TestConcurrentPoolAccess(t *testing.T) {
	p, err := New(Config{ChainID: "dev", MaxTransactions: 100, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx := transaction(t, 1)
	id, _ := tx.ID()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				if err := p.Add(ctx, tx); err != nil {
					t.Error(err)
				}
				if _, err := p.Select(ctx, 4096); err != nil {
					t.Error(err)
				}
				if err := p.Remove(ctx, []types.Hash{id}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
