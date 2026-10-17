package consensus

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"testing"
	"time"
)

func TestPoWRejectsMissingDependencies(t *testing.T) {
	if _, err := NewPoW(PoWConfig{Interval: "1ms", SyncInterval: "1ms"}, PoWDeps{}); err == nil {
		t.Fatal("accepted missing dependencies")
	}
}
func TestPoWWirePreservesBinaryID(t *testing.T) {
	want := []byte{0xff, 0, 0x80}
	b, err := json.Marshal(powMessage{Cursor: want})
	if err != nil {
		t.Fatal(err)
	}
	var got powMessage
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Cursor) != string(want) {
		t.Fatal("binary ID changed")
	}
}

type powTestProtocol struct{ core.WorkProtocol }

func (powTestProtocol) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "test", Version: "1", Network: "test"}
}
func (powTestProtocol) Parent(b core.Block) (core.ID, error) {
	if b.ID() == "genesis" {
		return "", nil
	}
	return "genesis", nil
}
func (powTestProtocol) EncodeBlock(b core.Block) ([]byte, error) { return []byte(b.ID()), nil }

type powTestLedger struct{ core.BranchLedger }

func (powTestLedger) Head(context.Context) (core.ID, error) { return "tip", nil }
func (powTestLedger) Block(_ context.Context, id core.ID) (core.Block, error) {
	return powTestBlock(id), nil
}

type powTestBlock core.ID

func (b powTestBlock) ID() core.ID { return core.ID(b) }

type powTestPool struct{ core.Mempool }

func (powTestPool) Candidates(context.Context) ([]core.Transaction, error) { return nil, nil }
func TestPoWHandlerDescriptorAndSideCursorFallback(t *testing.T) {
	p := &powConsensus{PoWDeps: PoWDeps{Protocol: powTestProtocol{}, Ledger: powTestLedger{}, Pool: powTestPool{}}}
	raw, _ := json.Marshal(powMessage{})
	if _, e := p.handle(context.Background(), raw); e == nil {
		t.Fatal("accepted wrong descriptor")
	}
	raw, _ = json.Marshal(powMessage{Descriptor: p.Protocol.Protocol(), Cursor: []byte("side-tip"), Locator: [][]byte{[]byte("genesis")}})
	out, e := p.handle(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	var response powMessage
	if e = json.Unmarshal(out, &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Blocks) != 1 || string(response.Blocks[0]) != "tip" {
		t.Fatalf("side cursor did not fall back to canonical locator: %+v", response)
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

type listPoWPool struct {
	core.Mempool
	txs []core.Transaction
}

func (p listPoWPool) Candidates(context.Context) ([]core.Transaction, error) { return p.txs, nil }
func TestPoWLargeTransactionBudgetAndRotation(t *testing.T) {
	p, _ := pow.New(pow.Config{ChainID: "large"})
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	large := pow.Transaction{ChainID: "large", Nonce: 1, Key: []byte("large"), Value: make([]byte, 1<<20)}
	large.Sign(key)
	bigTx, _ := pow.WrapTransaction(large)
	small := pow.Transaction{ChainID: "large", Nonce: 2, Key: []byte("small"), Value: []byte("v")}
	small.Sign(key)
	smallTx, _ := pow.WrapTransaction(small)
	c := &powConsensus{PoWDeps: PoWDeps{Protocol: p, Pool: listPoWPool{txs: []core.Transaction{bigTx, smallTx}}}}
	all, e := c.pendingBytes(context.Background(), 2<<20)
	if e != nil || len(all) != 2 {
		t.Fatal("maximal legal transaction omitted", e)
	}
	limited, e := c.pendingBytes(context.Background(), 1024)
	if e != nil || len(limited) != 1 {
		t.Fatal("oversized candidate blocked later small transaction", e)
	}
	if string(limited[0]) == string(all[0]) {
		t.Fatal("did not skip large item")
	}
}

type failingPoWLedger struct {
	core.BranchLedger
	err error
}

func (s failingPoWLedger) Head(context.Context) (core.ID, error) { return "", s.err }
func TestPoWSyncLocalFailureTyped(t *testing.T) {
	failure := errors.New("disk unavailable")
	p := &powConsensus{PoWDeps: PoWDeps{Protocol: powTestProtocol{}, Ledger: failingPoWLedger{err: failure}, Pool: powTestPool{}}}
	_, e := p.syncPeer(context.Background(), "peer", nil)
	var local *localPoWError
	if !errors.As(e, &local) || !errors.Is(e, failure) {
		t.Fatal("storage failure treated as remote error", e)
	}
}

type canceledPoWLedger struct{ core.BranchLedger }

func (canceledPoWLedger) Head(ctx context.Context) (core.ID, error) { return "", ctx.Err() }
func TestPoWHandlerRequestCancellationNotFatal(t *testing.T) {
	p := &powConsensus{PoWDeps: PoWDeps{Protocol: powTestProtocol{}, Ledger: canceledPoWLedger{}, Pool: powTestPool{}}, fatal: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, _ := json.Marshal(p.message())
	_, e := p.handle(ctx, raw)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	select {
	case e := <-p.fatal:
		t.Fatal("request cancellation stopped node", e)
	default:
	}
	if purePoWCancellation(errors.Join(context.Canceled, errors.New("disk failed"))) {
		t.Fatal("mixed error lost")
	}
	if purePoWCancellation(&core.CommittedError{Cause: context.Canceled}) {
		t.Fatal("committed failure lost")
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
	core.Network
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
	ex, _ := pow.NewExecutor(p)
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: pool, Executor: ex, Selector: sel})
	if e != nil {
		t.Fatal(e)
	}
	c, e := NewPoW(PoWConfig{Interval: "1ms", SyncInterval: "1h"}, PoWDeps{Protocol: p, Ledger: ledger, Builder: b, Executor: ex, Pool: pool, Network: nullPoWNetwork{descriptor: p.Protocol()}})
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
