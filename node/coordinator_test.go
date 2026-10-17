package node

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	builder "github.com/lengzhao/GoChainKit/plugins/builder/basic"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool/memory"
	store "github.com/lengzhao/GoChainKit/plugins/storage/memory"
	"testing"
)

type failingCommitter struct {
	core.Committer
	err error
}

func (f *failingCommitter) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	if f.err != nil {
		return f.err
	}
	return f.Committer.Commit(ctx, parent, b, r)
}
func TestCommitOrderingAndProtocolValidation(t *testing.T) {
	ctx := context.Background()
	protocol := testprotocol.New("custom")
	s, err := store.New(store.Config{}, store.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	p, err := pool.New(pool.Config{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, testprotocol.Tx{Label: "a", Body: []byte("value")}); err != nil {
		t.Fatal(err)
	}
	e := testprotocol.Executor{P: protocol}
	b, err := builder.New(builder.Config{MaxTransactionBytes: 4096}, builder.Deps{Protocol: protocol, Pool: p, Chain: s, States: s, Executor: e})
	if err != nil {
		t.Fatal(err)
	}
	commit := &failingCommitter{Committer: s}
	h, err := NewCoordinator(CoordinatorDeps{Protocol: protocol, Builder: b, Chain: s, States: s, Executor: e, Committer: commit, Pool: p})
	if err != nil {
		t.Fatal(err)
	}
	block, err := h.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := block.(testprotocol.Frame)
	bad.Commitment = "forged"
	if err := h.Validate(ctx, bad); err == nil {
		t.Fatal("accepted invalid commitment")
	}
	failure := errors.New("commit failed")
	commit.err = failure
	if err := h.Commit(ctx, block); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	left, _ := p.Select(ctx, 4096)
	if len(left) != 1 {
		t.Fatal("removed before commit")
	}
	commit.err = nil
	if err := h.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	left, _ = p.Select(ctx, 4096)
	if len(left) != 0 {
		t.Fatal("not removed")
	}
	if err := h.Commit(ctx, block); !errors.Is(err, core.ErrConflict) {
		t.Fatal("accepted stale block", err)
	}
}
