package mempool

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"sync"
	"testing"
)

func TestCapacitySelectionAndOwnership(t *testing.T) {
	ctx := context.Background()
	p, err := NewMemory(MemoryConfig{MaxTransactions: 1, MaxBytes: 4096}, Deps{testprotocol.New("custom")})
	if err != nil {
		t.Fatal(err)
	}
	tx := testprotocol.Tx{Label: "a", Body: []byte("value")}
	id := tx.ID()
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	tx.Body[0] = 'x'
	if err := p.Add(ctx, testprotocol.Tx{Label: "b"}); !errors.Is(err, core.ErrFull) {
		t.Fatal(err)
	}
	small, err := p.Select(ctx, 1)
	if err != nil || len(small) != 0 {
		t.Fatal("ignored budget", err)
	}
	got, err := p.Select(ctx, 4096)
	if err != nil || len(got) != 1 {
		t.Fatal(err)
	}
	v := got[0].(testprotocol.Tx)
	if string(v.Body) != "value" {
		t.Fatal("retained caller data")
	}
	v.Body[0] = 'x'
	got, err = p.Select(ctx, 4096)
	if err != nil || string(got[0].(testprotocol.Tx).Body) != "value" {
		t.Fatal("leaked internal data", err)
	}
	for range 2 {
		if err := p.Remove(ctx, []core.ID{id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Add(ctx, testprotocol.Tx{Label: "b"}); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidAndCanceled(t *testing.T) {
	p, err := NewMemory(MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, Deps{testprotocol.New("custom")})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(context.Background(), testprotocol.Tx{}); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Add(ctx, testprotocol.Tx{Label: "a"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NewMemory(MemoryConfig{}, Deps{}); err == nil {
		t.Fatal("accepted unbounded configuration")
	}
}
func TestConcurrentPoolAccess(t *testing.T) {
	p, err := NewMemory(MemoryConfig{MaxTransactions: 100, MaxBytes: 1 << 20}, Deps{testprotocol.New("custom")})
	if err != nil {
		t.Fatal(err)
	}
	tx := testprotocol.Tx{Label: "a"}
	ctx := context.Background()
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
				if err := p.Remove(ctx, []core.ID{tx.ID()}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
