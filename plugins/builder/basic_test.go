package builder

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	store "github.com/lengzhao/GoChainKit/plugins/storage"
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
	s, err := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	p, err := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(ctx, testprotocol.Tx{Label: "custom", Body: []byte("body")}); err != nil {
		t.Fatal(err)
	}
	selection, err := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: protocol})
	if err != nil {
		t.Fatal(err)
	}
	deps := Deps{Protocol: protocol, Pool: p, Ledger: s, Selector: selection, Executor: testprotocol.Executor{P: protocol}}
	b, err := NewBasic(BasicConfig{MaxTransactionBytes: 4096}, deps)
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
	remaining, err := p.Candidates(ctx)
	if err != nil || len(remaining) != 1 {
		t.Fatal("removed before commit", err)
	}
	failure := errors.New("executor failure")
	deps.Executor = failingExecutor{deps.Executor, failure}
	b, err = NewBasic(BasicConfig{MaxTransactionBytes: 4096}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Build(ctx, head); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	deps.Executor = testprotocol.Executor{P: testprotocol.New("other")}
	if _, err := NewBasic(BasicConfig{MaxTransactionBytes: 4096}, deps); err == nil {
		t.Fatal("accepted incompatible executor")
	}
}
