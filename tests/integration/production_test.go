package integration

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	consensus "github.com/lengzhao/GoChainKit/plugins/consensus/single"
	executor "github.com/lengzhao/GoChainKit/plugins/executor/kv"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
	"time"
)

type stopAfter struct {
	core.ConsensusHost
	cancel context.CancelFunc
	count  int
}

func (h *stopAfter) Commit(ctx context.Context, b core.Block) error {
	if err := h.ConsensusHost.Commit(ctx, b); err != nil {
		return err
	}
	h.count++
	if h.count == 2 {
		h.cancel()
	}
	return nil
}
func TestSingleNodeProduction(t *testing.T) {
	ctx := context.Background()
	graph := productionGraph(config.PluginUse{Use: "storage/memory"}, false)
	host, result, err := build.Build[core.ConsensusHost](ctx, graph, "host")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := build.RequireByID[core.Mempool](result, "pool", "pool")
	s, _ := build.RequireByID[core.ChainStore](result, "store", "store")
	states, _ := build.RequireByID[core.StateStore](result, "store", "store")
	tx := defaultTransaction(t, 1)
	if err := p.Add(ctx, tx); err != nil {
		t.Fatal(err)
	}
	block, err := host.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Validate(ctx, block); err != nil {
		t.Fatal(err)
	}
	if err := host.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	head, err := s.Head(ctx)
	if err != nil || head != block.ID() {
		t.Fatal(err)
	}
	r, err := s.Receipt(ctx, tx.ID())
	if err != nil || string(r.Data) != string([]byte{1}) {
		t.Fatal("missing default receipt", err)
	}
	snapshot, err := states.Snapshot(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kv.UnwrapTransaction(tx)
	if err != nil {
		t.Fatal(err)
	}
	value, err := snapshot.Get(ctx, executor.ValueKey(raw.Sender(), raw.Key))
	_ = snapshot.Close()
	if err != nil || string(value) != "world" {
		t.Fatal("state missing", err)
	}
	remaining, err := p.Select(ctx, 16384)
	if err != nil || len(remaining) != 0 {
		t.Fatal("not removed", err)
	}
	c, err := consensus.New(consensus.Config{Interval: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	deadline, timeout := context.WithTimeout(ctx, 2*time.Second)
	defer timeout()
	run, cancel := context.WithCancel(deadline)
	defer cancel()
	if err := c.Run(run, &stopAfter{ConsensusHost: host, cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	next, err := s.Head(ctx)
	if err != nil || next == head {
		t.Fatal("did not produce blocks", err)
	}
}
