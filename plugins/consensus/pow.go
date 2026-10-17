package consensus

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

type PoWConfig struct {
	Mining       *bool  `json:"mining,omitempty"`
	Interval     string `json:"interval"`
	SyncInterval string `json:"syncInterval"`
}
type PoWDeps struct {
	Protocol core.WorkProtocol `json:"protocol"`
	Ledger   core.BranchLedger `json:"ledger"`
	Builder  core.BlockBuilder `json:"builder"`
	Executor core.Executor     `json:"executor"`
	Pool     core.Mempool      `json:"pool"`
	Network  core.Network      `json:"network"`
}
type powConsensus struct {
	PoWDeps
	interval, syncInterval time.Duration
	mu                     sync.Mutex
	cancel                 context.CancelFunc
	mining                 bool
	fatal                  chan error
	gossipOffset           int
	fatalMu                sync.Mutex
	fatalErrors            []error
}
type powMessage struct {
	Descriptor   core.ProtocolDescriptor
	Cursor       []byte
	Locator      [][]byte
	Blocks       [][]byte
	Transactions [][]byte
}

func init() { pluginkit.Register("consensus/pow", NewPoW) }
func NewPoW(cfg PoWConfig, d PoWDeps) (core.Consensus, error) {
	i, e := time.ParseDuration(cfg.Interval)
	if e != nil || i <= 0 {
		return nil, fmt.Errorf("positive mining interval required")
	}
	s, e := time.ParseDuration(cfg.SyncInterval)
	if e != nil || s <= 0 {
		return nil, fmt.Errorf("positive sync interval required")
	}
	if d.Protocol == nil || d.Ledger == nil || d.Builder == nil || d.Executor == nil || d.Pool == nil || d.Network == nil {
		return nil, fmt.Errorf("pow dependencies required")
	}
	if e = core.RequireCompatible(d.Protocol.Protocol(), d.Ledger, d.Builder, d.Executor, d.Pool, d.Network); e != nil {
		return nil, e
	}
	if d.Ledger.LedgerID() == nil || d.Builder.LedgerID() != d.Ledger.LedgerID() {
		return nil, core.ErrLedgerMismatch
	}
	return &powConsensus{PoWDeps: d, interval: i, syncInterval: s, mining: cfg.Mining == nil || *cfg.Mining}, nil
}
func (p *powConsensus) message() powMessage { return powMessage{Descriptor: p.Protocol.Protocol()} }
func (p *powConsensus) canonical(ctx context.Context) ([]core.Block, error) {
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
		id, e = p.Protocol.Parent(b)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *powConsensus) handle(ctx context.Context, raw []byte) (out []byte, returnErr error) {
	defer func() {
		var local *localPoWError
		if !purePoWCancellation(returnErr) && errors.As(returnErr, &local) {
			p.reportFatal(local)
		}
	}()
	var req powMessage
	if e := json.Unmarshal(raw, &req); e != nil {
		return nil, e
	}
	if req.Descriptor != p.Protocol.Protocol() {
		return nil, fmt.Errorf("incompatible protocol")
	}
	if encodedSize(req.Transactions) > 2<<20 || len(req.Locator) > 128 {
		return nil, fmt.Errorf("oversized sync request")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range req.Transactions {
		tx, e := p.Protocol.DecodeTransaction(v)
		if e != nil {
			return nil, e
		}
		if e = p.Protocol.ValidateTransaction(ctx, tx); e != nil {
			return nil, e
		}
		if e = p.admit(ctx, tx); e != nil {
			return nil, e
		}
	}
	chain, e := p.canonical(ctx)
	if e != nil {
		return nil, localPoW(e)
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
		v, e := p.Protocol.EncodeBlock(chain[j])
		if e != nil {
			return nil, localPoW(e)
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
		return nil, localPoW(e)
	}
	return json.Marshal(resp)
}
func (p *powConsensus) importBlock(ctx context.Context, b core.Block, r *core.ExecutionResult) (returnErr error) {
	parent, e := p.Protocol.Parent(b)
	if e != nil {
		return e
	}
	pb, e := p.Ledger.Block(ctx, parent)
	if e != nil {
		if errors.Is(e, core.ErrNotFound) {
			return e
		}
		return localPoW(e)
	}
	if _, e = p.Protocol.Work(b); e != nil {
		return e
	}
	if e = p.Protocol.ValidateCandidate(ctx, supervisedPoWChain{p.Ledger}, pb, b); e != nil {
		return e
	}
	var result core.ExecutionResult
	if r != nil {
		result = *r
	} else {
		snap, e := p.Ledger.Snapshot(ctx, parent)
		if e != nil {
			return localPoW(e)
		}
		result, e = p.Executor.Execute(ctx, snap, b)
		closeErr := snap.Close()
		if e != nil {
			if errors.Is(e, core.ErrInvalidTransaction) || errors.Is(e, core.ErrDeferredTransaction) || errors.Is(e, context.Canceled) {
				return e
			}
			return localPoW(e)
		}
		if closeErr != nil {
			return localPoW(closeErr)
		}
		result.BlockID = b.ID()
	}
	if e = p.Protocol.ValidateBlock(ctx, supervisedPoWChain{p.Ledger}, pb, b, result); e != nil {
		return e
	}
	change, e := p.Ledger.Import(ctx, parent, b, result)
	if e != nil {
		if errors.Is(e, core.ErrConflict) || errors.Is(e, core.ErrAlreadyCommitted) || errors.Is(e, context.Canceled) {
			return e
		}
		return localPoW(e)
	}
	if change.OldHead == change.NewHead {
		return nil
	}
	if p.cancel != nil {
		defer p.cancel()
	}
	defer func() {
		if returnErr != nil {
			var committed *core.CommittedError
			if !errors.As(returnErr, &committed) {
				returnErr = &core.CommittedError{BlockID: b.ID(), Cause: returnErr}
			}
		}
	}()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	ctx = cleanup
	attached := map[core.ID]bool{}
	var ids []core.ID
	for _, block := range change.Attached {
		txs, e := p.Protocol.Transactions(block)
		if e != nil {
			return e
		}
		for _, tx := range txs {
			attached[tx.ID()] = true
			ids = append(ids, tx.ID())
		}
	}
	if e = p.Pool.Remove(ctx, ids); e != nil {
		return e
	}
	for j := len(change.Detached) - 1; j >= 0; j-- {
		block := change.Detached[j]
		txs, e := p.Protocol.Transactions(block)
		if e != nil {
			return e
		}
		for _, tx := range txs {
			_, confirmedErr := p.Ledger.Transaction(ctx, tx.ID())
			if confirmedErr != nil && !errors.Is(confirmedErr, core.ErrNotFound) {
				return &core.CommittedError{BlockID: b.ID(), Cause: confirmedErr}
			}
			if !attached[tx.ID()] && errors.Is(confirmedErr, core.ErrNotFound) {
				if e = p.Pool.Add(ctx, tx); e != nil && !errors.Is(e, core.ErrFull) && !errors.Is(e, core.ErrInvalidTransaction) {
					return e
				}
			}
		}
	}
	return nil
}
func (p *powConsensus) syncPeer(ctx context.Context, peer string, cursor []byte) ([]byte, error) {
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
		return cursor, localPoW(e)
	}
	raw, e := json.Marshal(req)
	if e != nil {
		return cursor, e
	}
	raw, e = p.Network.Request(ctx, peer, raw)
	if e != nil {
		return cursor, e
	}
	var resp powMessage
	if e = json.Unmarshal(raw, &resp); e != nil {
		return cursor, e
	}
	if resp.Descriptor != p.Protocol.Protocol() {
		return cursor, fmt.Errorf("incompatible protocol")
	}
	if len(resp.Blocks) > 32 || encodedSize(resp.Blocks) > 2*1024*1024 || encodedSize(resp.Transactions)+encodedSize(resp.Blocks) > 11<<18 {
		return cursor, fmt.Errorf("oversized block page")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range resp.Blocks {
		b, e := p.Protocol.DecodeBlock(v)
		if e != nil {
			return cursor, e
		}
		if e = p.importBlock(ctx, b, nil); e != nil && !errors.Is(e, core.ErrAlreadyCommitted) {
			return cursor, e
		}
		cursor = []byte(b.ID())
	}
	for _, v := range resp.Transactions {
		tx, e := p.Protocol.DecodeTransaction(v)
		if e != nil {
			return cursor, e
		}
		if e = p.Protocol.ValidateTransaction(ctx, tx); e != nil {
			return cursor, e
		}
		if e = p.admit(ctx, tx); e != nil {
			return cursor, e
		}
	}
	return cursor, nil
}
func (p *powConsensus) Run(ctx context.Context, h core.ConsensusHost) (returnErr error) {
	bound, ok := h.(core.LedgerBound)
	if !ok || bound.LedgerID() == nil || bound.LedgerID() != p.Ledger.LedgerID() {
		return core.ErrLedgerMismatch
	}
	p.fatal = make(chan error, 1)
	if e := p.Network.Handle(p.handle); e != nil {
		return e
	}
	run, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		p.fatalMu.Lock()
		defer p.fatalMu.Unlock()
		for _, e := range p.fatalErrors {
			returnErr = errors.Join(returnErr, e)
		}
	}()
	mine := time.NewTicker(p.interval)
	defer mine.Stop()
	syncTick := time.NewTicker(p.syncInterval)
	defer syncTick.Stop()
	done := make(chan struct{}, 1)
	fatal := p.fatal
	busy := false
	cursors := map[string][]byte{}
	for {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			if p.cancel != nil {
				p.cancel()
			}
			p.mu.Unlock()
			cancel()
			return ctx.Err()
		case e := <-fatal:
			cancel()
			return e
		case <-done:
			busy = false
		case <-mine.C:
			if busy || !p.mining {
				continue
			}
			p.mu.Lock()
			head, e := p.Ledger.Head(run)
			if e != nil {
				p.mu.Unlock()
				cancel()
				return e
			}
			mc, stop := context.WithCancel(run)
			p.cancel = stop
			p.mu.Unlock()
			busy = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer stop()
				b, r, e := p.Builder.Build(mc, head)
				if e == nil {
					p.mu.Lock()
					current, err := p.Ledger.Head(run)
					if err != nil {
						e = err
					}
					if err == nil && current == head {
						e = p.importBlock(run, b, &r)
					}
					p.mu.Unlock()
				}
				if e != nil && !transientPoW(e) {
					p.reportFatal(e)
				}
				select {
				case done <- struct{}{}:
				case <-run.Done():
				}
			}()
		case <-syncTick.C:
			for _, peer := range p.Network.Peers() {
				cursor, e := p.syncPeer(run, peer, cursors[peer])
				cursors[peer] = cursor
				if e != nil {
					var local *localPoWError
					var committed *core.CommittedError
					if errors.As(e, &local) || errors.As(e, &committed) {
						cancel()
						return e
					}
				}

			}
		}
	}
}

func encodedSize(values [][]byte) int {
	n := 0
	for _, v := range values {
		n += len(v)
	}
	return n
}

type localPoWError struct{ cause error }

func (e *localPoWError) Error() string { return "local PoW service failed: " + e.cause.Error() }
func (e *localPoWError) Unwrap() error { return e.cause }
func localPoW(e error) error {
	if purePoWCancellation(e) {
		return e
	}
	if e == nil {
		return nil
	}
	return &localPoWError{e}
}
func (p *powConsensus) reportFatal(e error) {
	p.fatalMu.Lock()
	p.fatalErrors = append(p.fatalErrors, e)
	p.fatalMu.Unlock()
	select {
	case p.fatal <- e:
	default:
	}
}
func (p *powConsensus) admit(ctx context.Context, tx core.Transaction) error {
	_, e := p.Ledger.Transaction(ctx, tx.ID())
	if e == nil {
		return nil
	}
	if !errors.Is(e, core.ErrNotFound) {
		return localPoW(e)
	}
	e = p.Pool.Add(ctx, tx)
	if e == nil || errors.Is(e, core.ErrFull) || errors.Is(e, core.ErrInvalidTransaction) {
		return nil
	}
	return localPoW(e)
}

// pendingBytes rotates the starting candidate so a full pool cannot starve
// later transactions. The request budget accommodates a maximal KV transaction.
func (p *powConsensus) pendingBytes(ctx context.Context, budget int) ([][]byte, error) {
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
		data, e := p.Protocol.EncodeTransaction(tx)
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
func transientPoW(e error) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *core.CommittedError, *localPoWError:
		return false
	case interface{ Unwrap() []error }:
		for _, child := range v.Unwrap() {
			if !transientPoW(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return transientPoW(v.Unwrap())
	}
	return e == context.Canceled || e == core.ErrConflict || e == core.ErrNoProposal || e == core.ErrAlreadyCommitted
}

func purePoWCancellation(e error) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *core.CommittedError, *core.TransactionError:
		return false
	case interface{ Unwrap() []error }:
		children := v.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !purePoWCancellation(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return purePoWCancellation(v.Unwrap())
	}
	return e == context.Canceled || e == context.DeadlineExceeded
}

// Preserve local lookup failures propagated through protocol reference checks.
type supervisedPoWChain struct{ core.ChainStore }

func (s supervisedPoWChain) Head(ctx context.Context) (core.ID, error) {
	id, e := s.ChainStore.Head(ctx)
	if e != nil && !errors.Is(e, core.ErrNotFound) {
		e = localPoW(e)
	}
	return id, e
}
func (s supervisedPoWChain) Block(ctx context.Context, id core.ID) (core.Block, error) {
	b, e := s.ChainStore.Block(ctx, id)
	if e != nil && !errors.Is(e, core.ErrNotFound) {
		e = localPoW(e)
	}
	return b, e
}
func (s supervisedPoWChain) Receipt(ctx context.Context, id core.ID) (core.Receipt, error) {
	r, e := s.ChainStore.Receipt(ctx, id)
	if e != nil && !errors.Is(e, core.ErrNotFound) {
		e = localPoW(e)
	}
	return r, e
}
