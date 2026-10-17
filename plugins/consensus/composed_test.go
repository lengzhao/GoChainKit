package consensus

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	powrules "github.com/lengzhao/GoChainKit/rules/pow"
	"sync/atomic"
	"testing"
	"time"
)

type countedSeal struct {
	core.WorkProtocol
	calls atomic.Int32
}

func (p *countedSeal) Seal(ctx context.Context, b core.Block) (core.Block, error) {
	p.calls.Add(1)
	return p.WorkProtocol.Seal(ctx, b)
}
func TestPoWMinerExplicitlySealsFinalizedProposal(t *testing.T) {
	model, _ := account.New(account.Config{Network: "seal"})
	rule, _ := powrules.New(powrules.Config{})
	protocol, e := composed.NewPoW(composed.Config{Network: "seal", GenesisTime: time.Now().Add(-time.Hour).Unix()}, model, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	p := &countedSeal{WorkProtocol: protocol}
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: protocol})
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: protocol})
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: protocol})
	builder, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: protocol, Ledger: ledger, Pool: pool, Executor: protocol, Selector: sel})
	if e != nil {
		t.Fatal(e)
	}
	miner, e := NewPoW(PoWConfig{Interval: "1ms", SyncInterval: "1h"}, PoWDeps{Protocol: p, Ledger: ledger, Builder: builder, Executor: protocol, Pool: pool, Network: nullPoWNetwork{descriptor: p.Protocol()}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- miner.Run(ctx, boundPoWHost{owner: ledger.LedgerID()}) }()
	g, _ := ledger.Head(ctx)
	for {
		head, err := ledger.Head(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if head != g {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("miner exited before commit: %v", err)
		case <-ctx.Done():
			t.Fatal("miner did not commit")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.calls.Load() == 0 {
		t.Fatal("miner never called Seal")
	}
}
