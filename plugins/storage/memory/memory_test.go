package memory

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"testing"
)

func TestCommitAndIsolation(t *testing.T) {
	ctx := context.Background()
	p, _ := protocol.New(protocol.Config{ChainID: "dev"})
	s, e := New(Config{}, Deps{p})
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
	r := contracts.ExecutionResult{Parent: head, BlockID: draft.ID(), Writes: writes, StateRoot: root}
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
	if e = s.Commit(ctx, head, b, r); !errors.Is(e, contracts.ErrAlreadyCommitted) {
		t.Fatal(e)
	}
}
