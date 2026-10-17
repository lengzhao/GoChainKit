package node

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"testing"
)

type chain struct {
	head    types.Hash
	block   types.Block
	err     error
	commits int
}

func (c *chain) Head(context.Context) (types.Hash, error)               { return c.head, nil }
func (c *chain) Block(context.Context, types.Hash) (types.Block, error) { return c.block, nil }
func (c *chain) Receipt(context.Context, types.Hash) (types.Receipt, error) {
	return types.Receipt{}, contracts.ErrNotFound
}
func (c *chain) Commit(_ context.Context, parent types.Hash, b types.Block, _ types.ExecutionResult) error {
	if c.err != nil {
		return c.err
	}
	if c.head != parent {
		return contracts.ErrConflict
	}
	c.head, _ = b.ID()
	c.block = b
	c.commits++
	return nil
}

type pool struct{ removed bool }

func (*pool) Add(context.Context, types.Transaction) error             { return nil }
func (*pool) Select(context.Context, int) ([]types.Transaction, error) { return nil, nil }
func (p *pool) Remove(context.Context, []types.Hash) error             { p.removed = true; return nil }

type states struct{}

func (states) Snapshot(context.Context, types.Hash) (contracts.StateReader, error) {
	return view{}, nil
}

type view struct{}

func (view) Entries(context.Context) (map[string][]byte, error) { return nil, nil }

func (view) Get(context.Context, []byte) ([]byte, error) { return nil, contracts.ErrNotFound }
func (view) Root() types.Hash                            { return types.StateRoot(nil) }
func (view) Close() error                                { return nil }

type execute struct{}

func (execute) Execute(_ context.Context, _ contracts.StateReader, b types.Block) (types.ExecutionResult, error) {
	return types.ExecutionResult{Parent: b.Header.Parent, StateRoot: types.StateRoot(nil)}, nil
}

type build struct{ b types.Block }

func (b build) Build(context.Context, types.Hash) (types.Block, types.ExecutionResult, error) {
	return b.b, types.ExecutionResult{}, nil
}
func TestCommitOrderingAndValidation(t *testing.T) {
	txRoot, _ := types.TransactionRoot(nil)
	parent := types.Block{Header: types.BlockHeader{Version: 1, ChainID: "dev", TransactionRoot: txRoot}}
	head, _ := parent.ID()
	candidate := types.Block{Header: types.BlockHeader{Version: 1, ChainID: "dev", Height: 1, Parent: head, TransactionRoot: txRoot, StateRoot: types.StateRoot(nil), ReceiptRoot: types.ReceiptRoot(nil)}}
	c := &chain{head: head, block: parent}
	p := &pool{}
	h, err := NewCoordinator(CoordinatorDeps{Builder: build{candidate}, Chain: c, States: states{}, Executor: execute{}, Committer: c, Pool: p})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b, err := h.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := b
	bad.Header.StateRoot = types.Hash{1}
	if err := h.Validate(ctx, bad); err == nil {
		t.Fatal("accepted wrong state root")
	}
	failure := errors.New("commit failed")
	c.err = failure
	if err := h.Commit(ctx, b); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if p.removed {
		t.Fatal("removed before commit")
	}
	c.err = nil
	if err := h.Commit(ctx, b); err != nil {
		t.Fatal(err)
	}
	if !p.removed || c.commits != 1 {
		t.Fatal("commit not applied")
	}
	if err := h.Commit(ctx, b); !errors.Is(err, contracts.ErrConflict) {
		t.Fatalf("stale proposal: %v", err)
	}
}
