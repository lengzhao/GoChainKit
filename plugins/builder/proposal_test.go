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

var errUnsealed = errors.New("unsealed proposal")

type proposalProtocol struct{ *testprotocol.Format }

func (p proposalProtocol) ValidateBlock(context.Context, core.ChainStore, core.Block, core.Block, core.ExecutionResult) error {
	return errUnsealed
}
func (p proposalProtocol) ValidateProposal(ctx context.Context, c core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	return p.Format.ValidateBlock(ctx, c, parent, b, r)
}
func TestBuilderFinalizesWithoutConsensusSeal(t *testing.T) {
	ctx := context.Background()
	p := proposalProtocol{testprotocol.New("proposal")}
	ledger, e := store.NewMemory(store.MemoryConfig{}, store.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	mem, e := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, pool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	sel, e := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	builder, e := NewBasic(BasicConfig{MaxTransactionBytes: 4096}, Deps{Protocol: p, Pool: mem, Ledger: ledger, Selector: sel, Executor: testprotocol.Executor{P: p}})
	if e != nil {
		t.Fatal(e)
	}
	parent, _ := ledger.Head(ctx)
	b, r, e := builder.Build(ctx, parent)
	if e != nil {
		t.Fatal("builder required seal", e)
	}
	if e = ledger.Commit(ctx, parent, b, r); !errors.Is(e, errUnsealed) {
		t.Fatal("storage accepted unsealed proposal", e)
	}
	head, _ := ledger.Head(ctx)
	if head != parent {
		t.Fatal("unsealed block advanced ledger")
	}
}
