package composed

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
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
	contextual, useContext := p.engine.(model.ContextualEngine)
	if requirement, ok := p.engine.(model.ContextRequirement); ok {
		useContext = useContext && requirement.RequiresExecutionContext()
	}
	if useContext {
		if history == nil {
			return core.ExecutionResult{}, fmt.Errorf("contextual model requires executor history")
		}
		parent, err := history.Block(ctx, core.ID(string(v.Header.Parent)))
		if err != nil {
			return core.ExecutionResult{}, err
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
	next, e := core.ApplyStateChanges(ctx, initial, transition.Writes, transition.Deletes)
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
		var to assetcap.Address
		copy(to[:], v.Header.Producer)
		if amount > 0 {
			data := append([]byte("gochainkit/composed/mint/v1\x00"), p.binding...)
			data = append(data, v.Header.Parent...)
			data = binary.BigEndian.AppendUint64(data, v.Header.Height)
			data = append(data, v.Header.Producer...)
			mint := assetcap.Mint{ID: core.ID(string(DoubleHash(data))), To: to, Amount: amount}
			reader := stateReader{next, core.Commitment(string(stateHash(next)))}
			var credit model.Transition
			if contextual, ok := currency.(assetcap.HeightCurrency); ok {
				credit, e = contextual.CreditAtHeight(ctx, reader, mint, v.Header.Height)
			} else {
				credit, e = currency.Credit(ctx, reader, mint)
			}
			if e != nil {
				return core.ExecutionResult{}, e
			}
			next, e = core.ApplyStateChanges(ctx, next, credit.Writes, credit.Deletes)
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

func (e executionContext) BlockHash(ctx context.Context, height uint64) (core.ID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	current := e.Height()
	if height >= current || current-height > 256 {
		return "", nil
	}
	b := e.parent
	for b != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		v, err := Unwrap(b)
		if err != nil {
			return "", err
		}
		if v.Header.Height == height {
			return b.ID(), nil
		}
		if v.Header.Height < height || len(v.Header.Parent) == 0 {
			return "", fmt.Errorf("execution ancestor height mismatch")
		}
		next, err := e.previous(ctx, b)
		if err != nil {
			return "", err
		}
		n, err := Unwrap(next)
		if err != nil {
			return "", err
		}
		if n.Header.Height+1 != v.Header.Height {
			return "", fmt.Errorf("execution ancestor discontinuity")
		}
		b = next
	}
	return "", fmt.Errorf("execution ancestor unavailable")
}

func (e executionContext) MedianTimePast(ctx context.Context) (int64, error) {
	times := make([]int64, 0, 11)
	b := e.parent
	for i := 0; i < 11 && b != nil; i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		v, err := Unwrap(b)
		if err != nil {
			return 0, err
		}
		times = append(times, v.Header.Timestamp)
		if v.Header.Height == 0 {
			break
		}
		b, err = e.previous(ctx, b)
		if err != nil {
			return 0, err
		}
	}
	if len(times) == 0 {
		return 0, fmt.Errorf("execution parent required")
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return times[len(times)/2], nil
}

// previous follows a known ancestor edge. It may use the underlying read-only
// chain directly because the caller cannot choose an arbitrary side-branch ID.
func (e executionContext) previous(ctx context.Context, b core.Block) (core.Block, error) {
	v, err := Unwrap(b)
	if err != nil {
		return nil, err
	}
	if v.Header.Height == 0 || len(v.Header.Parent) == 0 {
		return nil, fmt.Errorf("execution ancestor unavailable")
	}
	id := core.ID(string(v.Header.Parent))
	var next core.Block
	if h, ok := e.history.(executionHistory); ok {
		next, err = h.chain.Block(ctx, id)
	} else {
		next, err = e.history.Block(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	if next == nil || next.ID() != id {
		return nil, fmt.Errorf("execution ancestor identity mismatch")
	}
	n, err := Unwrap(next)
	if err != nil {
		return nil, err
	}
	if n.Header.Height != v.Header.Height-1 {
		return nil, fmt.Errorf("execution ancestor discontinuity")
	}
	return next, nil
}

// QueryContext binds read-only model calls to an existing block. The history is
// narrowed to that anchor, while BLOCKHASH starts from its actual predecessor.
func (p *Protocol) QueryContext(ctx context.Context, history model.BlockHistory, anchor core.ID) (model.ExecutionContext, error) {
	if history == nil {
		return nil, fmt.Errorf("query history required")
	}
	if err := core.RequireCompatible(p.Protocol(), history); err != nil {
		return nil, err
	}
	b, err := history.Block(ctx, anchor)
	if err != nil {
		return nil, err
	}
	if b == nil || b.ID() != anchor {
		return nil, fmt.Errorf("query anchor identity mismatch")
	}
	v, err := Unwrap(b)
	if err != nil {
		return nil, err
	}
	e := executionContext{candidate: b, history: executionHistory{chain: queryHistoryChain{history}, parent: anchor}, protocol: p}
	if v.Header.Height > 0 {
		e.parent, err = e.previous(ctx, b)
		if err != nil {
			return nil, err
		}
	}
	return e, nil
}

type queryHistoryChain struct{ model.BlockHistory }

func (queryHistoryChain) Receipt(context.Context, core.ID) (core.Receipt, error) {
	return core.Receipt{}, core.ErrNotFound
}
