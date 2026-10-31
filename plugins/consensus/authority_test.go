package consensus

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	choice "github.com/lengzhao/GoChainKit/choice/authority"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/routertest"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/replication"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/poa"
	seal "github.com/lengzhao/GoChainKit/seal/authority"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"github.com/lengzhao/GoChainKit/validator/static"
	"testing"
	"time"
)

func TestAuthorityObserverAndOwner(t *testing.T) {
	failure := errors.New("sync failed")
	p := powTestProtocol{}
	owner := &core.LedgerIdentity{}
	ledger := identityLedger{owner: owner}
	imp := identityImporter{owner: owner, descriptor: p.Protocol()}
	b := identityBuilder{owner: owner, descriptor: p.Protocol()}
	rep := failingReplication{ProtocolBound: imp, LedgerBound: imp, failure: failure}
	deps := AuthorityDeps{Protocol: descriptorOnly{p.Protocol()}, Ledger: ledger, Builder: b, Importer: imp, Replication: rep}
	c, e := NewAuthority(AuthorityConfig{Interval: "1ms"}, deps)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Run(context.Background(), boundPoWHost{owner: owner}); !errors.Is(e, failure) {
		t.Fatal("observer lost sync fault", e)
	}
	if e = c.Run(context.Background(), boundPoWHost{owner: &core.LedgerIdentity{}}); !errors.Is(e, core.ErrLedgerMismatch) {
		t.Fatal(e)
	}
	deps.Builder = identityBuilder{owner: &core.LedgerIdentity{}, descriptor: p.Protocol()}
	if _, e = NewAuthority(AuthorityConfig{Interval: "1ms"}, deps); !errors.Is(e, core.ErrLedgerMismatch) {
		t.Fatal("accepted mixed owners", e)
	}
}
func TestAuthorityShutdownPreservesCommittedFailure(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	members, _ := static.New(static.Config{Members: []valcap.Member{{ID: "a", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: key[32:]}}}})
	rule, _ := poa.New(poa.Config{GenesisTime: 100, SlotSeconds: 1, EpochSlots: 10}, poa.Deps{Members: members, Verifier: &suite.Verifier{}})
	clock := func() time.Time { return time.Unix(102, 0) }
	model, _ := account.New(account.Config{Network: "shutdown"})
	p, e := composed.New(composed.Config{Network: "shutdown", GenesisTime: 100, Producer: []byte("a"), Clock: clock}, model, rule, &choice.Choice{})
	if e != nil {
		t.Fatal(e)
	}
	keys := keystore.NewMemory()
	keys.Create(ctx, "consensus", key)
	signer, _ := custody.New(custody.Config{KeyID: "consensus"}, custody.Deps{Store: keys})
	producer, _ := seal.New(seal.Config{Clock: clock}, seal.Deps{Rule: rule, Signer: signer})
	ledger, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	base, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	failure := errors.New("cleanup failed")
	pool := &barrierCleanupPool{Mempool: base, entered: make(chan struct{}), release: make(chan struct{}), err: failure}
	ex, _ := composed.NewExecutor(p, ledger)
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: pool, Executor: ex, Selector: sel})
	if e != nil {
		t.Fatal(e)
	}
	imp, _ := replication.NewImporter(replication.ImportConfig{}, replication.ImportDeps{Rules: p, Ledger: ledger, Executor: ex, Pool: pool})
	rep, _ := replication.NewSync(replication.SyncConfig{Interval: "1h"}, replication.SyncDeps{Rules: p, Ledger: ledger, Pool: pool, Router: routertest.Start(t, nullPoWNetwork{descriptor: p.Protocol()}), Network: nullPoWNetwork{descriptor: p.Protocol()}, Importer: imp})
	c, e := NewAuthority(AuthorityConfig{Interval: "1ms"}, AuthorityDeps{Protocol: p, Ledger: ledger, Builder: b, Importer: imp, Replication: rep, Producer: producer})
	if e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(run, boundPoWHost{owner: ledger.LedgerID()}) }()
	select {
	case <-pool.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("commit not reached")
	}
	cancel()
	close(pool.release)
	select {
	case e = <-done:
		var committed *core.CommittedError
		if !errors.As(e, &committed) || !errors.Is(e, failure) {
			t.Fatal("lost committed cleanup failure", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown stuck")
	}
}

func TestAuthoritySlotBoundaryContinues(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))
	members, _ := static.New(static.Config{Members: []valcap.Member{{ID: "a", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: key[32:]}}, {ID: "b", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: other[32:]}}}})
	rule, _ := poa.New(poa.Config{GenesisTime: 100, SlotSeconds: 1, EpochSlots: 10}, poa.Deps{Members: members, Verifier: &suite.Verifier{}})
	now := int64(102)
	clock := func() time.Time { return time.Unix(now, 0) }
	model, _ := account.New(account.Config{Network: "shutdown"})
	p, e := composed.New(composed.Config{Network: "shutdown", GenesisTime: 100, Producer: []byte("a"), Clock: clock}, model, rule, &choice.Choice{})
	if e != nil {
		t.Fatal(e)
	}
	keys := keystore.NewMemory()
	keys.Create(ctx, "consensus", key)
	signer, _ := custody.New(custody.Config{KeyID: "consensus"}, custody.Deps{Store: keys})
	producer, _ := seal.New(seal.Config{Clock: clock}, seal.Deps{Rule: rule, Signer: signer})
	ledger, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	ex, _ := composed.NewExecutor(p, ledger)
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, _ := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 4096}, builder.Deps{Protocol: p, Ledger: ledger, Pool: pool, Executor: ex, Selector: sel})
	imp, _ := replication.NewImporter(replication.ImportConfig{}, replication.ImportDeps{Rules: p, Ledger: ledger, Executor: ex, Pool: pool})
	rep, _ := replication.NewSync(replication.SyncConfig{Interval: "1h"}, replication.SyncDeps{Rules: p, Ledger: ledger, Pool: pool, Router: routertest.Start(t, nullPoWNetwork{descriptor: p.Protocol()}), Network: nullPoWNetwork{descriptor: p.Protocol()}, Importer: imp}) // Start on member a's slot, then Draft crosses into member b's slot.
	now = 101
	entered := make(chan struct{})
	moving := &crossingProducer{AuthorityProducer: producer, advance: func() { now = 102; close(entered) }}
	c, e := NewAuthority(AuthorityConfig{Interval: "1ms"}, AuthorityDeps{Protocol: p, Ledger: ledger, Builder: b, Importer: imp, Replication: rep, Producer: moving})
	if e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(run, boundPoWHost{owner: ledger.LedgerID()}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("ready not called")
	}
	select {
	case e := <-done:
		t.Fatal("slot boundary terminated runtime", e)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if e = <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type crossingProducer struct {
	AuthorityProducer
	advance func()
	once    bool
}

func (p *crossingProducer) Ready(ctx context.Context, parent core.Block) error {
	e := p.AuthorityProducer.Ready(ctx, parent)
	if e == nil && !p.once {
		p.once = true
		p.advance()
	}
	return e
}

// descriptorOnly proves the scheduler does not require codecs or execution.
type descriptorOnly struct{ descriptor core.ProtocolDescriptor }

func (p descriptorOnly) Protocol() core.ProtocolDescriptor { return p.descriptor }
