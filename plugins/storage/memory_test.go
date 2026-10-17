package storage

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"testing"
)

func TestCommitAndIsolation(t *testing.T) {
	ctx := context.Background()
	p, _ := protocol.New(protocol.Config{ChainID: "dev"})
	s, e := NewMemory(MemoryConfig{}, Deps{p})
	if e != nil {
		t.Fatal(e)
	}
	head, _ := s.Head(ctx)
	parent, _ := s.Block(ctx, head)
	draft, e := p.Draft(ctx, parent, nil)
	if e != nil {
		t.Fatal(e)
	}
	writes := map[string][]byte{string([]byte{255, 0, 128}): {255, 0, 128}}
	root, _ := p.StateCommitment(ctx, writes)
	r := core.ExecutionResult{Parent: head, BlockID: draft.ID(), Writes: writes, StateRoot: root}
	b, e := p.Finalize(ctx, draft, r)
	if e != nil {
		t.Fatal(e)
	}
	r.BlockID = b.ID()
	if e = s.Commit(ctx, head, b, r); e != nil {
		t.Fatal(e)
	}
	writes[string([]byte{255, 0, 128})][0] = 0
	snap, _ := s.Snapshot(ctx, b.ID())
	v, e := snap.Get(ctx, []byte{255, 0, 128})
	if e != nil || v[0] != 255 {
		t.Fatal("snapshot alias", e)
	}
	if e = s.Commit(ctx, head, b, r); !errors.Is(e, core.ErrAlreadyCommitted) {
		t.Fatal(e)
	}
}

func TestMemoryLedgerIdentity(t *testing.T) {
	p, _ := protocol.New(protocol.Config{ChainID: "identity"})
	a, e := NewMemory(MemoryConfig{}, Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	b, e := NewMemory(MemoryConfig{}, Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	if a.LedgerID() == nil || a.LedgerID() == b.LedgerID() {
		t.Fatal("ledger instances must have distinct nonnil owner tokens")
	}
	if a.LedgerID() != a.LedgerID() {
		t.Fatal("unstable ledger token")
	}
}
