package basic

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool/memory"
	store "github.com/lengzhao/GoChainKit/plugins/storage/memory"
	"testing"
)

type failingExecutor struct {
	core.Executor
	err error
}

func (e failingExecutor) Execute(context.Context, core.StateReader, core.Block) (core.ExecutionResult, error) {
	return core.ExecutionResult{}, e.err
}
func TestBuildCustomFormatWithoutRemoving(t *testing.T) {
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
	if err := p.Add(ctx, testprotocol.Tx{Label: "custom", Body: []byte("body")}); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Protocol: protocol, Pool: p, Chain: s, States: s, Executor: testprotocol.Executor{P: protocol}}
	b, err := New(Config{MaxTransactionBytes: 4096}, deps)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := s.Head(ctx)
	block, result, err := b.Build(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	frame := block.(testprotocol.Frame)
	if frame.Revision != 1 || frame.Ancestor != head || len(frame.Records) != 1 || result.BlockID != block.ID() {
		t.Fatal("bad candidate")
	}
	remaining, err := p.Select(ctx, 4096)
	if err != nil || len(remaining) != 1 {
		t.Fatal("removed before commit", err)
	}
	failure := errors.New("executor failure")
	deps.Executor = failingExecutor{deps.Executor, failure}
	b, err = New(Config{MaxTransactionBytes: 4096}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Build(ctx, head); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	deps.Executor = testprotocol.Executor{P: testprotocol.New("other")}
	if _, err := New(Config{MaxTransactionBytes: 4096}, deps); err == nil {
		t.Fatal("accepted incompatible executor")
	}
}
