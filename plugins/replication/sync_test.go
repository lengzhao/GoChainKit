package replication

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	replicationcap "github.com/lengzhao/GoChainKit/cap/replication"
	"github.com/lengzhao/GoChainKit/core"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"testing"
	"time"
)

func TestPoWWirePreservesBinaryID(t *testing.T) {
	want := []byte{0xff, 0, 0x80}
	b, err := json.Marshal(syncMessage{Cursor: want})
	if err != nil {
		t.Fatal(err)
	}
	var got syncMessage
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Cursor) != string(want) {
		t.Fatal("binary ID changed")
	}
}

type powTestProtocol struct{ consensuscap.WorkProtocol }

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

type powTestLedger struct{ chaincap.BranchLedger }

func (powTestLedger) Head(context.Context) (core.ID, error) { return "tip", nil }
func (powTestLedger) Block(_ context.Context, id core.ID) (core.Block, error) {
	return powTestBlock(id), nil
}

type powTestBlock core.ID

func (b powTestBlock) ID() core.ID { return core.ID(b) }

type powTestPool struct{ core.Mempool }

func (powTestPool) Candidates(context.Context) ([]core.Transaction, error) { return nil, nil }
func TestPoWHandlerDescriptorAndSideCursorFallback(t *testing.T) {
	p := &chainSync{SyncDeps: SyncDeps{Rules: powTestProtocol{}, Ledger: powTestLedger{}, Pool: powTestPool{}}}
	raw, _ := json.Marshal(syncMessage{})
	if _, e := p.handle(context.Background(), raw); e == nil {
		t.Fatal("accepted wrong descriptor")
	}
	raw, _ = json.Marshal(syncMessage{Descriptor: p.Rules.Protocol(), Cursor: []byte("side-tip"), Locator: [][]byte{[]byte("genesis")}})
	out, e := p.handle(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	var response syncMessage
	if e = json.Unmarshal(out, &response); e != nil {
		t.Fatal(e)
	}
	if len(response.Blocks) != 1 || string(response.Blocks[0]) != "tip" {
		t.Fatalf("side cursor did not fall back to canonical locator: %+v", response)
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
	c := &chainSync{SyncDeps: SyncDeps{Rules: p, Pool: listPoWPool{txs: []core.Transaction{bigTx, smallTx}}}}
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
	chaincap.BranchLedger
	err error
}

func (s failingPoWLedger) Head(context.Context) (core.ID, error) { return "", s.err }
func TestPoWSyncLocalFailureTyped(t *testing.T) {
	failure := errors.New("disk unavailable")
	p := &chainSync{SyncDeps: SyncDeps{Rules: powTestProtocol{}, Ledger: failingPoWLedger{err: failure}, Pool: powTestPool{}}}
	_, e := p.syncPeer(context.Background(), "peer", nil)
	var local *replicationcap.LocalServiceError
	if !errors.As(e, &local) || !errors.Is(e, failure) {
		t.Fatal("storage failure treated as remote error", e)
	}
}

type canceledPoWLedger struct{ chaincap.BranchLedger }

func (canceledPoWLedger) Head(ctx context.Context) (core.ID, error) { return "", ctx.Err() }
func TestPoWHandlerRequestCancellationNotFatal(t *testing.T) {
	p := &chainSync{SyncDeps: SyncDeps{Rules: powTestProtocol{}, Ledger: canceledPoWLedger{}, Pool: powTestPool{}}, fatal: make(chan error, 1)}
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
	if pureCancellation(errors.Join(context.Canceled, errors.New("disk failed"))) {
		t.Fatal("mixed error lost")
	}
	if pureCancellation(&core.CommittedError{Cause: context.Canceled}) {
		t.Fatal("committed failure lost")
	}
}

// Shutdown must join in-flight handlers so a local failure cannot disappear
// merely because the root cancellation raced it.
type handlerNetwork struct {
	networkcap.Network
	installed chan func(context.Context, []byte) ([]byte, error)
}

func (n handlerNetwork) Handle(h func(context.Context, []byte) ([]byte, error)) error {
	n.installed <- h
	return nil
}
func (handlerNetwork) Peers() []string { return nil }

type barrierHeadLedger struct {
	chaincap.BranchLedger
	entered, release chan struct{}
	failure          error
}

func (l barrierHeadLedger) Head(context.Context) (core.ID, error) {
	close(l.entered)
	<-l.release
	return "", l.failure
}
func TestSyncShutdownJoinsHandlerLocalFailure(t *testing.T) {
	failure := errors.New("local read failed while stopping")
	entered, release := make(chan struct{}), make(chan struct{})
	network := handlerNetwork{installed: make(chan func(context.Context, []byte) ([]byte, error), 1)}
	p := &chainSync{SyncDeps: SyncDeps{Rules: powTestProtocol{}, Ledger: barrierHeadLedger{entered: entered, release: release, failure: failure}, Pool: powTestPool{}, Network: network}, interval: time.Hour, fatal: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	handler := <-network.installed
	raw, _ := json.Marshal(p.message())
	handled := make(chan struct{})
	go func() { defer close(handled); handler(context.Background(), raw) }()
	<-entered
	cancel()
	select {
	case e := <-done:
		close(release)
		<-handled
		t.Fatalf("Run returned before handler completed: %v", e)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-handled
	if e := <-done; !errors.Is(e, failure) {
		t.Fatalf("handler failure lost: %v", e)
	}
	if _, e := handler(context.Background(), raw); !errors.Is(e, core.ErrClosed) {
		t.Fatalf("stopped handler accepted work: %v", e)
	}
	if e := p.Run(context.Background()); !errors.Is(e, core.ErrClosed) {
		t.Fatalf("service accepted second Run: %v", e)
	}
}

type admissionLedger struct {
	chaincap.BranchLedger
	failure error
}

func (l admissionLedger) Transaction(context.Context, core.ID) (core.ConfirmedTransaction, error) {
	return core.ConfirmedTransaction{}, l.failure
}
func TestSyncAdmissionMixedExpectedErrorIsLocal(t *testing.T) {
	failure := errors.New("local admission failed")
	for _, test := range []struct{ lookup, add error }{
		{lookup: errors.Join(core.ErrNotFound, failure)},
		{lookup: core.ErrNotFound, add: errors.Join(core.ErrFull, failure)},
		{lookup: core.ErrNotFound, add: errors.Join(core.ErrInvalidTransaction, failure)},
	} {
		p := &chainSync{SyncDeps: SyncDeps{Ledger: admissionLedger{failure: test.lookup}, Pool: failingAddPool{failure: test.add}}}
		err := p.admit(context.Background(), powTestBlock("tx"))
		var local *replicationcap.LocalServiceError
		if !errors.As(err, &local) || !errors.Is(err, failure) {
			t.Fatalf("admission fault hidden by expected error: %v", err)
		}
	}
}

type importPageProtocol struct{ powTestProtocol }

func (importPageProtocol) DecodeBlock(data []byte) (core.Block, error) {
	return powTestBlock(core.ID(data)), nil
}

type importPageNetwork struct{ networkcap.Network }

func (importPageNetwork) Request(context.Context, string, []byte) ([]byte, error) {
	return json.Marshal(syncMessage{Descriptor: powTestProtocol{}.Protocol(), Blocks: [][]byte{[]byte("received")}})
}

type importPageFailure struct {
	replicationcap.BlockImporter
	failure error
}

func (i importPageFailure) Import(context.Context, core.Block, *core.ExecutionResult) error {
	return i.failure
}
func TestSyncImportMixedAlreadyCommittedRemainsLocal(t *testing.T) {
	failure := errors.New("commit database failed")
	for _, test := range []struct {
		err   error
		local bool
	}{
		{err: core.ErrAlreadyCommitted},
		{err: errors.Join(core.ErrAlreadyCommitted)},
		{err: &replicationcap.LocalServiceError{Cause: errors.Join(core.ErrAlreadyCommitted, failure)}, local: true},
	} {
		p := &chainSync{SyncDeps: SyncDeps{Rules: importPageProtocol{}, Ledger: powTestLedger{}, Pool: powTestPool{}, Network: importPageNetwork{}, Importer: importPageFailure{failure: test.err}}}
		_, err := p.syncPeer(context.Background(), "peer", nil)
		if test.local {
			var local *replicationcap.LocalServiceError
			if !errors.As(err, &local) || !errors.Is(err, failure) {
				t.Fatalf("local import fault swallowed as duplicate: %v", err)
			}
		} else if err != nil {
			t.Fatalf("pure duplicate rejected: %v", err)
		}
	}
}
