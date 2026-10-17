package consensus

import (
	"context"
	"errors"
	"fmt"
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/replication"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"testing"
	"time"
)

func TestPoWRejectsMissingDependencies(t *testing.T) {
	if _, err := NewPoW(PoWConfig{Interval: "1ms"}, PoWDeps{}); err == nil {
		t.Fatal("accepted missing dependencies")
	}
}
func TestPoWCommittedCancellationIsFatal(t *testing.T) {
	if transientPoW(&core.CommittedError{BlockID: "committed", Cause: context.Canceled}) {
		t.Fatal("committed maintenance failure swallowed as cancellation")
	}
	if transientPoW(errors.Join(context.Canceled, errors.New("disk failure"))) {
		t.Fatal("mixed cancellation swallowed")
	}
	if !transientPoW(fmt.Errorf("mining canceled: %w", context.Canceled)) {
		t.Fatal("pure cancellation not retryable")
	}
}

type barrierCleanupPool struct {
	core.Mempool
	entered, release chan struct{}
	err              error
}

func (p *barrierCleanupPool) Remove(context.Context, []core.ID) error {
	close(p.entered)
	<-p.release
	return p.err
}

type nullPoWNetwork struct {
	networkcap.Network
	descriptor core.ProtocolDescriptor
}

func (n nullPoWNetwork) Protocol() core.ProtocolDescriptor                        { return n.descriptor }
func (nullPoWNetwork) Handle(func(context.Context, []byte) ([]byte, error)) error { return nil }
func (nullPoWNetwork) Peers() []string                                            { return nil }

type boundPoWHost struct {
	core.ConsensusHost
	owner *core.LedgerIdentity
}

func (h boundPoWHost) LedgerID() *core.LedgerIdentity { return h.owner }
func TestPoWShutdownPreservesCommittedCleanupFailure(t *testing.T) {
	p, _ := pow.New(pow.Config{ChainID: "shutdown"})
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	base, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	failure := errors.New("cleanup failed after shutdown")
	pool := &barrierCleanupPool{Mempool: base, entered: make(chan struct{}), release: make(chan struct{}), err: failure}
	baseProtocol, _ := kv.New(kv.Config{ChainID: p.Protocol().Network})
	inner, _ := kv.NewExecutor(baseProtocol)
	ex, _ := pow.NewExecutor(p, inner)
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: pool, Executor: ex, Selector: sel})
	if e != nil {
		t.Fatal(e)
	}
	imp, e := replication.NewImporter(replication.ImportConfig{}, replication.ImportDeps{Rules: p, Ledger: ledger, Executor: ex, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	rep, e := replication.NewSync(replication.SyncConfig{Interval: "1h"}, replication.SyncDeps{Rules: p, Ledger: ledger, Pool: pool, Network: nullPoWNetwork{descriptor: p.Protocol()}, Importer: imp})
	if e != nil {
		t.Fatal(e)
	}
	c, e := NewPoW(PoWConfig{Interval: "1ms"}, PoWDeps{Protocol: p, Ledger: ledger, Builder: b, Importer: imp, Replication: rep})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, boundPoWHost{owner: ledger.LedgerID()}) }()
	select {
	case <-pool.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("did not reach committed cleanup")
	}
	cancel()
	close(pool.release)
	select {
	case e := <-done:
		var committed *core.CommittedError
		if !errors.As(e, &committed) || !errors.Is(e, failure) {
			t.Fatal("shutdown lost committed error", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown stuck")
	}
}

type powTestProtocol struct{ consensuscap.WorkProtocol }

func (powTestProtocol) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "test", Version: "1", Network: "test"}
}
