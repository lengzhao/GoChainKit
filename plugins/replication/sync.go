package replication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"sync"
	"time"
)

type SyncProtocol interface {
	core.TransactionProtocol
	core.BlockCodec
	Parent(core.Block) (core.ID, error)
}
type SyncConfig struct {
	Interval string `json:"interval"`
}

// SyncLedger is a read-only chain/index view; imports go through BlockImporter.
type SyncLedger interface {
	core.ChainStore
	core.TransactionStore
	core.LedgerBound
}
type SyncDeps struct {
	Rules    SyncProtocol       `json:"protocol"`
	Ledger   SyncLedger         `json:"ledger"`
	Pool     core.Mempool       `json:"pool"`
	Network  core.Network       `json:"network"`
	Importer core.BlockImporter `json:"importer"`
}
type chainSync struct {
	SyncDeps
	interval     time.Duration
	mu           sync.Mutex
	fatal        chan error
	fatalMu      sync.Mutex
	fatalErrors  []error
	gossipOffset int
	runMu        sync.Mutex
	used         bool
	accepting    bool
	runContext   context.Context
	handlers     sync.WaitGroup
}

func init() { pluginkit.Register("sync/chain", NewSync) }
func NewSync(c SyncConfig, d SyncDeps) (core.Replicator, error) {
	interval, e := time.ParseDuration(c.Interval)
	if e != nil || interval <= 0 {
		return nil, fmt.Errorf("positive sync interval required")
	}
	if d.Rules == nil || d.Ledger == nil || d.Pool == nil || d.Network == nil || d.Importer == nil {
		return nil, fmt.Errorf("sync dependencies required")
	}
	if e = core.RequireCompatible(d.Rules.Protocol(), d.Ledger, d.Pool, d.Network, d.Importer); e != nil {
		return nil, e
	}
	if d.Ledger.LedgerID() == nil || d.Importer.LedgerID() != d.Ledger.LedgerID() {
		return nil, core.ErrLedgerMismatch
	}
	return &chainSync{SyncDeps: d, interval: interval, fatal: make(chan error, 1)}, nil
}
func (p *chainSync) Protocol() core.ProtocolDescriptor { return p.Rules.Protocol() }
func (p *chainSync) LedgerID() *core.LedgerIdentity    { return p.Ledger.LedgerID() }
func (p *chainSync) Run(ctx context.Context) (returnErr error) {
	run, cancel := context.WithCancel(ctx)
	p.runMu.Lock()
	if p.used {
		p.runMu.Unlock()
		cancel()
		return core.ErrClosed
	}
	p.used = true
	p.accepting = true
	p.runContext = run
	p.runMu.Unlock()
	defer func() {
		// Add and the transition to stopped share one gate, so Wait cannot race
		// a new handler. Requests already queued on mu are included in the join.
		p.runMu.Lock()
		p.accepting = false
		p.runMu.Unlock()
		cancel()
		p.handlers.Wait()
		p.fatalMu.Lock()
		defer p.fatalMu.Unlock()
		returnErr = errors.Join(append([]error{returnErr}, p.fatalErrors...)...)
	}()
	if e := p.Network.Handle(p.serve); e != nil {
		return e
	}
	tick := time.NewTicker(p.interval)
	defer tick.Stop()

	cursors := map[string][]byte{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-p.fatal:
			return e
		case <-tick.C:
			for _, peer := range p.Network.Peers() {
				cursor, e := p.syncPeer(ctx, peer, cursors[peer])
				cursors[peer] = cursor
				if e != nil {
					var local *core.LocalServiceError
					var committed *core.CommittedError
					if errors.As(e, &local) || errors.As(e, &committed) {
						return e
					}
				}
			}
		}
	}
}

// serve links request cancellation with service shutdown and rejects new work
// after Run stops. Network retains the registered handler until its own Stop.
func (p *chainSync) serve(ctx context.Context, raw []byte) ([]byte, error) {
	p.runMu.Lock()
	if !p.accepting {
		p.runMu.Unlock()
		return nil, core.ErrClosed
	}
	p.handlers.Add(1)
	run := p.runContext
	p.runMu.Unlock()
	defer p.handlers.Done()
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(run, cancel)
	defer stop()
	defer cancel()
	return p.handle(request, raw)
}
func (p *chainSync) reportFatal(e error) {
	p.fatalMu.Lock()
	p.fatalErrors = append(p.fatalErrors, e)
	p.fatalMu.Unlock()
	select {
	case p.fatal <- e:
	default:
	}
}

type syncMessage struct {
	Descriptor   core.ProtocolDescriptor
	Cursor       []byte
	Locator      [][]byte
	Blocks       [][]byte
	Transactions [][]byte
}

func (p *chainSync) message() syncMessage { return syncMessage{Descriptor: p.Rules.Protocol()} }
func (p *chainSync) canonical(ctx context.Context) ([]core.Block, error) {
	id, e := p.Ledger.Head(ctx)
	if e != nil {
		return nil, e
	}
	var out []core.Block
	seen := map[core.ID]bool{}
	for id != "" {
		if seen[id] {
			return nil, fmt.Errorf("ancestry cycle")
		}
		seen[id] = true
		b, e := p.Ledger.Block(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
		id, e = p.Rules.Parent(b)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *chainSync) handle(ctx context.Context, raw []byte) (out []byte, returnErr error) {
	defer func() {
		var local *core.LocalServiceError
		if !pureCancellation(returnErr) && errors.As(returnErr, &local) {
			p.reportFatal(local)
		}
	}()
	var req syncMessage
	if e := json.Unmarshal(raw, &req); e != nil {
		return nil, e
	}
	if req.Descriptor != p.Rules.Protocol() {
		return nil, fmt.Errorf("incompatible protocol")
	}
	if encodedSize(req.Transactions) > 2<<20 || len(req.Locator) > 128 {
		return nil, fmt.Errorf("oversized sync request")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	for _, v := range req.Transactions {
		tx, e := p.Rules.DecodeTransaction(v)
		if e != nil {
			return nil, e
		}
		if e = p.Rules.ValidateTransaction(ctx, tx); e != nil {
			return nil, e
		}
		if e = p.admit(ctx, tx); e != nil {
			return nil, e
		}
	}
	chain, e := p.canonical(ctx)
	if e != nil {
		return nil, localService(e)
	}
	index := len(chain)
	for _, loc := range req.Locator {
		for j, b := range chain {
			if b.ID() == core.ID(loc) && j < index {
				index = j
			}
		}
	}
	if len(req.Cursor) > 0 {
		for j, b := range chain {
			if b.ID() == core.ID(req.Cursor) {
				index = j
				break
			}
		}
	}
	resp := p.message()
	for j := index - 1; j >= 0 && len(resp.Blocks) < 32; j-- {
		v, e := p.Rules.EncodeBlock(chain[j])
		if e != nil {
			return nil, localService(e)
		}
		if encodedSize(resp.Blocks)+len(v) > 2*1024*1024 {
			if len(resp.Blocks) == 0 {
				return nil, fmt.Errorf("block exceeds sync page budget")
			}
			break
		}
		resp.Blocks = append(resp.Blocks, v)
		resp.Cursor = []byte(chain[j].ID())
	}
	resp.Transactions, e = p.pendingBytes(ctx, (11<<18)-encodedSize(resp.Blocks))
	if e != nil {
		return nil, localService(e)
	}
	return json.Marshal(resp)
}
func (p *chainSync) syncPeer(ctx context.Context, peer string, cursor []byte) ([]byte, error) {
	p.mu.Lock()
	chain, e := p.canonical(ctx)
	req := p.message()
	req.Cursor = cursor
	if e == nil {
		for j, step := 0, 1; j < len(chain); j, step = j+step, step*2 {
			req.Locator = append(req.Locator, []byte(chain[j].ID()))
		}
		if len(chain) > 0 {
			req.Locator = append(req.Locator, []byte(chain[len(chain)-1].ID()))
		}
		req.Transactions, e = p.pendingBytes(ctx, 2<<20)
	}
	p.mu.Unlock()
	if e != nil {
		return cursor, localService(e)
	}
	raw, e := json.Marshal(req)
	if e != nil {
		return cursor, e
	}
	raw, e = p.Network.Request(ctx, peer, raw)
	if e != nil {
		return cursor, e
	}
	var resp syncMessage
	if e = json.Unmarshal(raw, &resp); e != nil {
		return cursor, e
	}
	if resp.Descriptor != p.Rules.Protocol() {
		return cursor, fmt.Errorf("incompatible protocol")
	}
	if len(resp.Blocks) > 32 || encodedSize(resp.Blocks) > 2*1024*1024 || encodedSize(resp.Transactions)+encodedSize(resp.Blocks) > 11<<18 {
		return cursor, fmt.Errorf("oversized block page")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range resp.Blocks {
		b, e := p.Rules.DecodeBlock(v)
		if e != nil {
			return cursor, e
		}
		if e = p.Importer.Import(ctx, b, nil); e != nil && !onlyExpected(e, func(e error) bool { return e == core.ErrAlreadyCommitted }) {
			return cursor, e
		}
		cursor = []byte(b.ID())
	}
	for _, v := range resp.Transactions {
		tx, e := p.Rules.DecodeTransaction(v)
		if e != nil {
			return cursor, e
		}
		if e = p.Rules.ValidateTransaction(ctx, tx); e != nil {
			return cursor, e
		}
		if e = p.admit(ctx, tx); e != nil {
			return cursor, e
		}
	}
	return cursor, nil
}
func encodedSize(values [][]byte) int {
	n := 0
	for _, v := range values {
		n += len(v)
	}
	return n
}

func (p *chainSync) admit(ctx context.Context, tx core.Transaction) error {
	_, e := p.Ledger.Transaction(ctx, tx.ID())
	if e == nil {
		return nil
	}
	if !onlyExpected(e, func(e error) bool { return e == core.ErrNotFound }) {
		return localService(e)
	}
	e = p.Pool.Add(ctx, tx)
	if e == nil || onlyExpected(e, func(e error) bool { return e == core.ErrFull || e == core.ErrInvalidTransaction }) {
		return nil
	}
	return localService(e)
}

// pendingBytes rotates the starting candidate so a full pool cannot starve
// later transactions. The request budget accommodates a maximal KV transaction.
func (p *chainSync) pendingBytes(ctx context.Context, budget int) ([][]byte, error) {
	txs, e := p.Pool.Candidates(ctx)
	if e != nil {
		return nil, e
	}
	if len(txs) == 0 {
		return nil, nil
	}
	start := p.gossipOffset % len(txs)
	p.gossipOffset = (start + 1) % len(txs)
	var out [][]byte
	used := 0
	for i := 0; i < len(txs); i++ {
		tx := txs[(start+i)%len(txs)]
		data, e := p.Rules.EncodeTransaction(tx)
		if e != nil {
			return nil, e
		}
		if len(data) > budget-used {
			continue
		}
		out = append(out, data)
		used += len(data)
	}
	return out, nil
}
