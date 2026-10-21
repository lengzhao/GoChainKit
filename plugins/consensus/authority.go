package consensus

import (
	"context"
	"errors"
	"fmt"
	replicationcap "github.com/lengzhao/GoChainKit/cap/replication"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"time"
)

// AuthorityProducer combines local scheduling and short proof production only.
// Validation and synchronization remain independent and work without a signer.
type AuthorityProducer interface {
	Seal(context.Context, core.Block, core.Block) (core.Block, error)
	Ready(context.Context, core.Block) error
}
type AuthorityConfig struct {
	Interval string `json:"interval"`
}
type AuthorityDeps struct {
	Protocol    core.ProtocolBound           `json:"protocol"`
	Ledger      core.LedgerReader            `json:"ledger"`
	Builder     core.BlockBuilder            `json:"builder"`
	Importer    replicationcap.BlockImporter `json:"importer"`
	Replication replicationcap.Replicator    `json:"replication"`
	Producer    AuthorityProducer            `json:"producer,omitempty"`
}
type authorityConsensus struct {
	deps     AuthorityDeps
	interval time.Duration
}

func NewAuthority(c AuthorityConfig, d AuthorityDeps) (core.Consensus, error) {
	interval, e := time.ParseDuration(c.Interval)
	if e != nil || interval <= 0 {
		return nil, fmt.Errorf("positive authority polling interval required")
	}
	if d.Protocol == nil || d.Ledger == nil || d.Builder == nil || d.Importer == nil || d.Replication == nil {
		return nil, fmt.Errorf("authority dependencies required")
	}
	if e = core.RequireCompatible(d.Protocol.Protocol(), d.Ledger, d.Builder, d.Importer, d.Replication); e != nil {
		return nil, e
	}
	owner := d.Ledger.LedgerID()
	if owner == nil || d.Builder.LedgerID() != owner || d.Importer.LedgerID() != owner || d.Replication.LedgerID() != owner {
		return nil, core.ErrLedgerMismatch
	}
	return &authorityConsensus{d, interval}, nil
}
func init() { pluginkit.Register("consensus/authority", NewAuthority) }
func (a *authorityConsensus) Run(ctx context.Context, h core.ConsensusHost) (returnErr error) {
	bound, ok := h.(core.LedgerBound)
	if !ok || bound.LedgerID() != a.deps.Ledger.LedgerID() {
		return core.ErrLedgerMismatch
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		e := a.deps.Replication.Run(run)
		if e == nil && run.Err() == nil {
			e = fmt.Errorf("replication stopped unexpectedly")
		}
		done <- e
	}()
	received := false
	defer func() {
		cancel()
		if !received {
			e := <-done
			if !pureCancellation(e) {
				returnErr = errors.Join(returnErr, e)
			}
		}
	}()
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-done:
			received = true
			return e
		case <-ticker.C:
			if a.deps.Producer == nil {
				continue
			}
			e := a.produce(run)
			if !transientProduction(e) {
				return e
			}
		}
	}
}
func (a *authorityConsensus) produce(ctx context.Context) error {
	head, e := a.deps.Ledger.Head(ctx)
	if e != nil {
		return e
	}
	parent, e := a.deps.Ledger.Block(ctx, head)
	if e != nil {
		return e
	}
	if e = a.deps.Producer.Ready(ctx, parent); e != nil {
		return e
	}
	b, r, e := a.deps.Builder.Build(ctx, head)
	if e != nil {
		return e
	}
	b, e = a.deps.Producer.Seal(ctx, parent, b)
	if e != nil {
		return e
	}
	r.BlockID = b.ID()
	current, e := a.deps.Ledger.Head(ctx)
	if e != nil {
		return e
	}
	if current != head {
		return core.ErrConflict
	}
	return a.deps.Importer.Import(ctx, b, &r)
}
