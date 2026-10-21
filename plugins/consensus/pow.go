package consensus

import (
	"context"
	"errors"
	"fmt"
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	replicationcap "github.com/lengzhao/GoChainKit/cap/replication"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"sync"
	"time"
)

type PoWConfig struct {
	Mining   *bool  `json:"mining,omitempty"`
	Interval string `json:"interval"`
}
type PoWDeps struct {
	Protocol    consensuscap.WorkProtocol    `json:"protocol"`
	Ledger      core.LedgerReader            `json:"ledger"`
	Builder     core.BlockBuilder            `json:"builder"`
	Importer    replicationcap.BlockImporter `json:"importer"`
	Replication replicationcap.Replicator    `json:"replication"`
}
type powConsensus struct {
	PoWDeps
	interval time.Duration
	mining   bool
}

func init() { pluginkit.Register("consensus/pow", NewPoW) }
func NewPoW(c PoWConfig, d PoWDeps) (core.Consensus, error) {
	interval, e := time.ParseDuration(c.Interval)
	if e != nil || interval <= 0 {
		return nil, fmt.Errorf("positive mining interval required")
	}
	if d.Protocol == nil || d.Ledger == nil || d.Builder == nil || d.Importer == nil || d.Replication == nil {
		return nil, fmt.Errorf("pow dependencies required")
	}
	if e = core.RequireCompatible(d.Protocol.Protocol(), d.Ledger, d.Builder, d.Importer, d.Replication); e != nil {
		return nil, e
	}
	owner := d.Ledger.LedgerID()
	if owner == nil || d.Builder.LedgerID() != owner || d.Importer.LedgerID() != owner || d.Replication.LedgerID() != owner {
		return nil, core.ErrLedgerMismatch
	}
	return &powConsensus{PoWDeps: d, interval: interval, mining: c.Mining == nil || *c.Mining}, nil
}
func (p *powConsensus) Run(ctx context.Context, h core.ConsensusHost) (returnErr error) {
	bound, ok := h.(core.LedgerBound)
	if !ok || bound.LedgerID() != p.Ledger.LedgerID() {
		return core.ErrLedgerMismatch
	}
	run, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	// Each worker publishes exactly one result. Joining on shutdown preserves
	// committed maintenance failures even when cancellation wins the select.
	replicationDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		e := p.Replication.Run(run)
		if e == nil && run.Err() == nil {
			e = fmt.Errorf("replication stopped unexpectedly")
		}
		replicationDone <- e
	}()
	var mineDone chan error
	var stopMining context.CancelFunc
	defer func() {
		cancel()
		if stopMining != nil {
			stopMining()
		}
		wg.Wait()
		select {
		case e := <-replicationDone:
			if !pureCancellation(e) {
				returnErr = errors.Join(returnErr, e)
			}
		default:
		}
		if mineDone != nil {
			select {
			case e := <-mineDone:
				if !transientProduction(e) {
					returnErr = errors.Join(returnErr, e)
				}
			default:
			}
		}
	}()
	tick := time.NewTicker(p.interval)
	defer tick.Stop()
	var miningParent core.ID
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-replicationDone:
			if pureCancellation(e) && ctx.Err() != nil {
				return ctx.Err()
			}
			return e
		case e := <-mineDone:
			mineDone = nil
			stopMining = nil
			if !transientProduction(e) {
				return e
			}
		case <-tick.C:
			if !p.mining {
				continue
			}
			head, e := p.Ledger.Head(run)
			if e != nil {
				return e
			}
			if mineDone != nil {
				if head != miningParent {
					stopMining()
				}
				continue
			}
			miningParent = head
			mc, stop := context.WithCancel(run)
			stopMining = stop
			completed := make(chan error, 1)
			mineDone = completed
			wg.Add(1)
			go func(head core.ID) {
				defer wg.Done()
				defer stop()
				b, r, e := p.Builder.Build(mc, head)
				if e == nil {
					b, e = p.Protocol.Seal(mc, b)
					if e == nil {
						r.BlockID = b.ID()
					}
				}
				if e == nil {
					current, err := p.Ledger.Head(run)
					if err != nil {
						e = err
					} else if current == head {
						e = p.Importer.Import(run, b, &r)
					}
				}
				completed <- e
			}(head)
		}
	}
}
