package consensus

import (
	"context"
	"errors"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
)

type failingReplication struct {
	core.ProtocolBound
	core.LedgerBound
	failure error
}

func (r failingReplication) Run(context.Context) error { return r.failure }
func TestPoWDependsOnIndependentReplication(t *testing.T) {
	// A replication service fault must terminate the mining supervisor, even
	// with mining disabled; no real network or mining work is required.
	failure := errors.New("independent replication failed")
	p := powTestProtocol{}
	owner := &core.LedgerIdentity{}
	ledger := identityLedger{owner: owner}
	importer := identityImporter{owner: owner, descriptor: p.Protocol()}
	builder := identityBuilder{owner: owner, descriptor: p.Protocol()}
	rep := failingReplication{ProtocolBound: importer, LedgerBound: importer, failure: failure}
	mining := false
	c, e := NewPoW(PoWConfig{Interval: "1ms", Mining: &mining}, PoWDeps{Protocol: p, Ledger: ledger, Builder: builder, Importer: importer, Replication: rep})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Run(context.Background(), boundPoWHost{owner: owner}); !errors.Is(e, failure) {
		t.Fatalf("lost injected worker error: %v", e)
	}
}

type identityLedger struct {
	chaincap.BranchLedger
	owner *core.LedgerIdentity
}

func (l identityLedger) LedgerID() *core.LedgerIdentity  { return l.owner }
func (identityLedger) Protocol() core.ProtocolDescriptor { return powTestProtocol{}.Protocol() }

type identityImporter struct {
	owner      *core.LedgerIdentity
	descriptor core.ProtocolDescriptor
}

func (i identityImporter) Protocol() core.ProtocolDescriptor                             { return i.descriptor }
func (i identityImporter) LedgerID() *core.LedgerIdentity                                { return i.owner }
func (identityImporter) Import(context.Context, core.Block, *core.ExecutionResult) error { return nil }

type identityBuilder struct {
	core.BlockBuilder
	owner      *core.LedgerIdentity
	descriptor core.ProtocolDescriptor
}

func (b identityBuilder) Protocol() core.ProtocolDescriptor { return b.descriptor }
func (b identityBuilder) LedgerID() *core.LedgerIdentity    { return b.owner }
