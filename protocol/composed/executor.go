package composed

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/asset"
	"sort"
)

type stateReader struct {
	state map[string][]byte
	root  core.Commitment
}

func (s stateReader) Get(ctx context.Context, k []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, ok := s.state[string(k)]
	if !ok {
		return nil, core.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s stateReader) Entries(ctx context.Context) (map[string][]byte, error) {
	return core.ApplyStateChanges(ctx, s.state, nil, nil)
}
func (s stateReader) Root() core.Commitment { return s.root }
func (s stateReader) Close() error          { return nil }

// Execute runs model semantics independently of consensus proof production.
func (executor *Executor) Execute(ctx context.Context, s core.StateReader, b core.Block) (core.ExecutionResult, error) {
	p, history := executor.protocol, executor.history
	v, e := Unwrap(b)
	if e != nil {
		return core.ExecutionResult{}, e
	}
	txs, e := p.Transactions(b)
	if e != nil {
		return core.ExecutionResult{}, e
	}
	initial, e := s.Entries(ctx)
	if e != nil {
		return core.ExecutionResult{}, e
	}
	var transition model.Transition
	if contextual, ok := p.engine.(model.ContextualEngine); ok {
		if history == nil {
			return core.ExecutionResult{}, fmt.Errorf("contextual model requires executor history")
		}
		parent, e := history.Block(ctx, core.ID(string(v.Header.Parent)))
		if e != nil {
			return core.ExecutionResult{}, e
		}
		parentEnvelope, err := Unwrap(parent)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		parentEnvelope.Proof = nil
		stableParent, err := Wrap(parentEnvelope)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		stableEnvelope := clone(v)
		stableEnvelope.Proof = nil
		stableEnvelope.Header.StateRoot = nil
		stableEnvelope.Header.ReceiptsRoot = nil
		stableEnvelope.Header.Consensus = nil
		stableEnvelope.Header.Consensus, err = p.rule.Draft(ctx, parentEnvelope, stableEnvelope.Header)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		stableCandidate, err := Wrap(stableEnvelope)
		if err != nil {
			return core.ExecutionResult{}, err
		}
		transition, e = contextual.ExecuteInContext(ctx, s, txs, executionContext{parent: stableParent, candidate: stableCandidate, history: executionHistory{chain: history, parent: parent.ID()}, protocol: p})
	} else {
		transition, e = p.engine.Execute(ctx, s, txs)
	}
	if e != nil {
		return core.ExecutionResult{}, e
	}
	next, e := model.Apply(ctx, initial, transition)
	if e != nil {
		return core.ExecutionResult{}, e
	}
	if p.economics != nil {
		economics := p.economics
		currency := p.currency
		reward, e := economics.Reward(v.Header.Height)
		if e != nil {
			return core.ExecutionResult{}, e
		}
		fees, e := asset.Fees(transition)
		if e != nil {
			return core.ExecutionResult{}, e
		}
		amount, e := asset.Add(reward, fees)
		if e != nil {
			return core.ExecutionResult{}, e
		}
		if len(v.Header.Producer) != 32 {
			return core.ExecutionResult{}, fmt.Errorf("currency producer must be a 32-byte address")
		}
		var to asset.Address
		copy(to[:], v.Header.Producer)
		if amount > 0 {
			data := append([]byte("gochainkit/composed/mint/v1\x00"), p.binding...)
			data = append(data, v.Header.Parent...)
			data = binary.BigEndian.AppendUint64(data, v.Header.Height)
			data = append(data, v.Header.Producer...)
			credit, e := currency.Credit(ctx, stateReader{next, core.Commitment(string(stateHash(next)))}, asset.Mint{ID: core.ID(string(DoubleHash(data))), To: to, Amount: amount})
			if e != nil {
				return core.ExecutionResult{}, e
			}
			next, e = model.Apply(ctx, next, credit)
			if e != nil {
				return core.ExecutionResult{}, e
			}
		}
	}
	writes := map[string][]byte{}
	var deletes []string
	for k, v := range next {
		old, exists := initial[k]
		if !exists || !bytes.Equal(v, old) {
			writes[k] = bytes.Clone(v)
		}
	}
	for k := range initial {
		if _, exists := next[k]; !exists {
			deletes = append(deletes, k)
		}
	}
	sort.Strings(deletes)
	root := stateHash(next)
	r := core.ExecutionResult{Parent: core.ID(string(v.Header.Parent)), BlockID: b.ID(), Writes: writes, Deletes: deletes, StateRoot: core.Commitment(string(root))}
	for _, receipt := range transition.Receipts {
		r.Receipts = append(r.Receipts, core.Receipt{TransactionID: receipt.TransactionID, Data: bytes.Clone(receipt.Data)})
	}
	return r, nil
}

// Executor supplies optional model history without introducing a protocol-ledger
// construction cycle. Its public capability is execution, not ledger mutation.
type Executor struct {
	protocol *Protocol
	history  core.ChainStore
}

// NewExecutor accepts this protocol family's capability without exposing its
// concrete base object through the plugin dependency graph.
func NewExecutor(protocol core.Protocol, history core.ChainStore) (*Executor, error) {
	provider, ok := protocol.(interface{ executionProtocol() *Protocol })
	if !ok || history == nil {
		return nil, fmt.Errorf("composed protocol and history required")
	}
	p := provider.executionProtocol()
	if p == nil {
		return nil, fmt.Errorf("nil composed protocol")
	}
	if err := core.RequireCompatible(p.Protocol(), history); err != nil {
		return nil, err
	}
	return &Executor{p, readOnlyHistory{history}}, nil
}
func (e *Executor) Protocol() core.ProtocolDescriptor { return e.protocol.Protocol() }

type readOnlyHistory struct{ core.ChainStore }
type executionContext struct {
	parent, candidate core.Block
	history           model.BlockHistory
	protocol          *Protocol
}

func (e executionContext) Parent() core.Block          { return e.parent }
func (e executionContext) Candidate() core.Block       { return e.candidate }
func (e executionContext) History() model.BlockHistory { return e.history }
func (p *Protocol) executionProtocol() *Protocol       { return p }

func (e executionContext) StateCommitment(ctx context.Context, state map[string][]byte) (core.Commitment, error) {
	return e.protocol.StateCommitment(ctx, state)
}

// Execution history is anchored to the execution parent. Auxiliary certificates
// are excluded from model-visible blocks because valid signer subsets may differ.
type executionHistory struct {
	chain  core.ChainStore
	parent core.ID
}

func (h executionHistory) Protocol() core.ProtocolDescriptor         { return h.chain.Protocol() }
func (h executionHistory) Head(ctx context.Context) (core.ID, error) { return h.parent, ctx.Err() }
func (h executionHistory) Block(ctx context.Context, id core.ID) (core.Block, error) {
	cursor := h.parent
	seen := map[core.ID]bool{}
	for cursor != "" {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if seen[cursor] {
			return nil, fmt.Errorf("execution history cycle")
		}
		seen[cursor] = true
		block, e := h.chain.Block(ctx, cursor)
		if e != nil {
			return nil, e
		}
		v, e := Unwrap(block)
		if e != nil {
			return nil, e
		}
		if cursor == id {
			v.Proof = nil
			return Wrap(v)
		}
		cursor = core.ID(string(v.Header.Parent))
	}
	return nil, core.ErrNotFound
}
func (e executionContext) Height() uint64   { v, _ := Unwrap(e.candidate); return v.Header.Height }
func (e executionContext) Timestamp() int64 { v, _ := Unwrap(e.candidate); return v.Header.Timestamp }
