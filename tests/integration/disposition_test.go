package integration

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/consensus"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
	"time"
)

type fillGapHost struct {
	core.ConsensusHost
	pool   core.Mempool
	next   core.Transaction
	cancel context.CancelFunc
	count  int
}

func (h *fillGapHost) Commit(ctx context.Context, b core.Block) error {
	if err := h.ConsensusHost.Commit(ctx, b); err != nil {
		return err
	}
	h.count++
	if h.count == 1 {
		if err := h.pool.Add(ctx, h.next); err != nil {
			return err
		}
	}
	if h.count == 3 {
		h.cancel()
	}
	return nil
}
func TestDeferredAndRejectedTransactions(t *testing.T) {
	ctx := context.Background()
	graph := productionGraph(config.PluginUse{Use: "storage/memory"}, false)
	host, result, err := build.Build[core.ConsensusHost](ctx, graph, "host")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := build.RequireByID[core.Mempool](result, "pool", "pool")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := build.RequireByID[core.ChainStore](result, "store", "store")
	if err != nil {
		t.Fatal(err)
	}
	later := defaultTransaction(t, 2)
	ready := defaultTransaction(t, 1)
	if err := pool.Add(ctx, later); err != nil {
		t.Fatal(err)
	}
	c, err := consensus.NewSingle(consensus.SingleConfig{Interval: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	deadline, timeout := context.WithTimeout(ctx, 2*time.Second)
	defer timeout()
	run, cancel := context.WithCancel(deadline)
	defer cancel()
	wrapped := &fillGapHost{ConsensusHost: host, pool: pool, next: ready, cancel: cancel}
	if err := c.Run(run, wrapped); !errors.Is(err, context.Canceled) || wrapped.count != 3 {
		t.Fatal("deferred transaction stopped chain", err)
	}
	for _, tx := range []core.Transaction{ready, later} {
		if _, err := chain.Receipt(ctx, tx.ID()); err != nil {
			t.Fatal("ready transaction not confirmed", err)
		}
	}
	left, err := pool.Candidates(ctx)
	if err != nil || len(left) != 0 {
		t.Fatal("pool not cleaned", err)
	}
	if err := pool.Add(ctx, ready); err != nil {
		t.Fatal(err)
	}
	block, err := host.Propose(ctx)
	if err != nil {
		t.Fatal("stale transaction poisoned proposal", err)
	}
	raw, err := kv.UnwrapBlock(block)
	if err != nil || len(raw.Transactions) != 0 {
		t.Fatal("replayed transaction included", err)
	}
	if err := host.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	left, err = pool.Candidates(ctx)
	if err != nil || len(left) != 0 {
		t.Fatal("stale transaction not evicted", err)
	}
}
