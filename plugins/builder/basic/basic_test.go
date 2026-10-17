package basic

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"testing"
)

type pool struct {
	tx      types.Transaction
	removed bool
}

func (p *pool) Add(context.Context, types.Transaction) error { return nil }
func (p *pool) Select(context.Context, int) ([]types.Transaction, error) {
	return []types.Transaction{p.tx.Clone()}, nil
}
func (p *pool) Remove(context.Context, []types.Hash) error { p.removed = true; return nil }

type store struct{ block types.Block }

func (s store) Head(context.Context) (types.Hash, error)               { return s.block.ID() }
func (s store) Block(context.Context, types.Hash) (types.Block, error) { return s.block.Clone(), nil }
func (s store) Receipt(context.Context, types.Hash) (types.Receipt, error) {
	return types.Receipt{}, contracts.ErrNotFound
}

type snapshot struct{}

func (snapshot) Entries(context.Context) (map[string][]byte, error) { return nil, nil }

func (snapshot) Get(context.Context, []byte) ([]byte, error) { return nil, contracts.ErrNotFound }
func (snapshot) Root() types.Hash                            { return types.StateRoot(nil) }
func (snapshot) Close() error                                { return nil }

type states struct{}

func (states) Snapshot(context.Context, types.Hash) (contracts.StateReader, error) {
	return snapshot{}, nil
}

type executor struct{ fail error }

func (e executor) Execute(_ context.Context, _ contracts.StateReader, b types.Block) (types.ExecutionResult, error) {
	return types.ExecutionResult{Parent: b.Header.Parent, StateRoot: types.StateRoot(nil)}, e.fail
}
func TestBuildIncludesTransactionsWithoutRemoving(t *testing.T) {
	tx := types.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("a")}
	if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	root, _ := types.TransactionRoot(nil)
	s := store{types.Block{Header: types.BlockHeader{Version: 1, ChainID: "dev", TransactionRoot: root}}}
	head, _ := s.Head(context.Background())
	p := &pool{tx: tx}
	b, err := New(Config{ChainID: "dev", MaxTransactionBytes: 4096}, Deps{Pool: p, Chain: s, States: states{}, Executor: executor{}})
	if err != nil {
		t.Fatal(err)
	}
	block, result, err := b.Build(context.Background(), head)
	if err != nil {
		t.Fatal(err)
	}
	if block.Header.Height != 1 || block.Header.Parent != head || len(block.Transactions) != 1 {
		t.Fatal("incorrect proposal")
	}
	id, err := block.ID()
	if err != nil {
		t.Fatal(err)
	}
	if result.BlockID != id {
		t.Fatal("execution result not bound")
	}
	if p.removed {
		t.Fatal("removed uncommitted transaction")
	}
}
func TestBuildPropagatesExecutionFailure(t *testing.T) {
	root, _ := types.TransactionRoot(nil)
	s := store{types.Block{Header: types.BlockHeader{Version: 1, ChainID: "dev", TransactionRoot: root}}}
	head, _ := s.Head(context.Background())
	failure := errors.New("execution failed")
	tx := types.Transaction{ChainID: "dev", Nonce: 1, Key: []byte("a")}
	_ = tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32)))
	b, err := New(Config{ChainID: "dev", MaxTransactionBytes: 4096}, Deps{Pool: &pool{tx: tx}, Chain: s, States: states{}, Executor: executor{failure}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Build(context.Background(), head); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
