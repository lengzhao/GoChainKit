package composed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/jsoncodec"

	"math"
	"math/big"
	"sort"
	"time"
)

type Config struct {
	Network     string
	GenesisTime int64
	Producer    []byte
	Clock       func() time.Time
}
type Protocol struct {
	cfg        Config
	engine     model.Engine
	economics  Economics
	currency   assetcap.Currency
	rule       Rule
	choice     Choice
	descriptor core.ProtocolDescriptor
	binding    []byte
	genesis    Envelope
	state      map[string][]byte
}
type block struct {
	value Envelope
	id    core.ID
}

func (b *block) ID() core.ID { return b.id }
func DoubleHash(data []byte) []byte {
	a := sha256.Sum256(data)
	b := sha256.Sum256(a[:])
	return bytes.Clone(b[:])
}
func (e Envelope) ID() core.ID {
	data, _ := json.Marshal(e.Header)
	return core.ID(string(DoubleHash(append([]byte("gochainkit/composed/block/v1\x00"), data...))))
}
func clone(e Envelope) Envelope {
	e.Header.Binding = bytes.Clone(e.Header.Binding)
	e.Header.Parent = bytes.Clone(e.Header.Parent)
	e.Header.TransactionsRoot = bytes.Clone(e.Header.TransactionsRoot)
	e.Header.StateRoot = bytes.Clone(e.Header.StateRoot)
	e.Header.ReceiptsRoot = bytes.Clone(e.Header.ReceiptsRoot)
	e.Header.Producer = bytes.Clone(e.Header.Producer)
	e.Header.Consensus = bytes.Clone(e.Header.Consensus)
	e.Proof = bytes.Clone(e.Proof)
	if e.Transactions != nil {
		txs := make([][]byte, len(e.Transactions))
		for i, tx := range e.Transactions {
			txs[i] = bytes.Clone(tx)
		}
		e.Transactions = txs
	}
	return e
}

func Wrap(e Envelope) (core.Block, error) {
	if e.Header.Version != 1 || len(e.Header.Binding) != 32 {
		return nil, fmt.Errorf("invalid composed envelope")
	}
	e = clone(e)
	return &block{e, e.ID()}, nil
}
func Unwrap(b core.Block) (Envelope, error) {
	v, ok := b.(*block)
	if !ok || v == nil {
		return Envelope{}, fmt.Errorf("not a composed block")
	}
	return clone(v.value), nil
}
func New(c Config, engine model.Engine, rule Rule, choice Choice) (*Protocol, error) {
	if c.Network == "" || c.GenesisTime <= 0 || engine == nil || rule == nil || choice == nil || rule.ID() == "" || choice.ID() == "" {
		return nil, fmt.Errorf("composed configuration required")
	}
	if engine.Protocol().Network != c.Network {
		return nil, fmt.Errorf("model network mismatch")
	}
	if e := engine.Protocol().Validate(); e != nil {
		return nil, e
	}
	var economics Economics
	var currency assetcap.Currency
	if candidate, ok := rule.(Economics); ok && candidate.Enabled() {
		economics = candidate
		var compatible bool
		currency, compatible = engine.(assetcap.Currency)
		if !compatible {
			return nil, fmt.Errorf("economic rules require currency capability")
		}
	}
	state, e := engine.Genesis(context.Background())
	if e != nil {
		return nil, e
	}
	state, e = core.ApplyStateChanges(context.Background(), state, nil, nil)
	if e != nil {
		return nil, e
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	c.Producer = bytes.Clone(c.Producer)
	root := stateHash(state)
	bindingData, e := json.Marshal(struct {
		Network      string
		GenesisTime  int64
		Model        core.ProtocolDescriptor
		State        []byte
		Rule, Choice string
	}{c.Network, c.GenesisTime, engine.Protocol(), root, rule.ID(), choice.ID()})
	if e != nil {
		return nil, e
	}
	binding := DoubleHash(bindingData)
	p := &Protocol{cfg: c, engine: engine, economics: economics, currency: currency, rule: rule, choice: choice, binding: binding, state: state, descriptor: core.ProtocolDescriptor{Name: "composed", Version: "1/" + hex.EncodeToString(binding), Network: c.Network}}
	p.genesis = Envelope{Header: Header{Version: 1, Binding: bytes.Clone(binding), Timestamp: c.GenesisTime, StateRoot: root, TransactionsRoot: transactionRoot(nil), ReceiptsRoot: receiptRoot(nil)}}
	p.genesis.Header.Consensus, e = rule.Genesis(p.genesis.Header)
	if e != nil {
		return nil, e
	}
	return p, nil
}
func (p *Protocol) Protocol() core.ProtocolDescriptor { return p.descriptor }
func (p *Protocol) PolicyID() string                  { return p.choice.ID() }
func (p *Protocol) EncodeTransaction(t core.Transaction) ([]byte, error) {
	return p.engine.EncodeTransaction(t)
}
func (p *Protocol) DecodeTransaction(v []byte) (core.Transaction, error) {
	return p.engine.DecodeTransaction(bytes.Clone(v))
}
func (p *Protocol) ValidateTransaction(ctx context.Context, t core.Transaction) error {
	return p.engine.ValidateTransaction(ctx, t)
}
func (p *Protocol) EncodeBlock(b core.Block) ([]byte, error) {
	e, err := Unwrap(b)
	if err != nil {
		return nil, err
	}
	return json.Marshal(e)
}
func (p *Protocol) DecodeBlock(raw []byte) (core.Block, error) {
	if len(raw) > 4<<20 {
		return nil, core.ErrFull
	}
	var e Envelope
	if err := jsoncodec.Canonical(raw, &e); err != nil {
		return nil, err
	}
	if !bytes.Equal(e.Header.Binding, p.binding) {
		return nil, fmt.Errorf("foreign chain binding")
	}
	return Wrap(e)
}
func (p *Protocol) Transactions(b core.Block) ([]core.Transaction, error) {
	e, err := Unwrap(b)
	if err != nil {
		return nil, err
	}
	out := make([]core.Transaction, len(e.Transactions))
	for i, data := range e.Transactions {
		out[i], err = p.engine.DecodeTransaction(bytes.Clone(data))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (p *Protocol) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	state, e := core.ApplyStateChanges(ctx, p.state, nil, nil)
	if e != nil {
		return nil, nil, e
	}
	b, e := Wrap(p.genesis)
	return b, state, e
}
func (p *Protocol) Parent(b core.Block) (core.ID, error) {
	e, err := Unwrap(b)
	return core.ID(string(e.Header.Parent)), err
}
func (p *Protocol) Draft(ctx context.Context, parent core.Block, txs []core.Transaction) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	old, e := Unwrap(parent)
	if e != nil {
		return nil, e
	}
	if old.Header.Height == math.MaxUint64 || !bytes.Equal(old.Header.Binding, p.binding) {
		return nil, fmt.Errorf("invalid parent")
	}
	// Auxiliary parent certificates may vary without changing parent identity.
	old.Proof = nil
	b := Envelope{Header: Header{Version: 1, Binding: bytes.Clone(p.binding), Parent: []byte(parent.ID()), Height: old.Header.Height + 1, Timestamp: p.cfg.Clock().Unix(), Producer: bytes.Clone(p.cfg.Producer)}}
	if b.Header.Timestamp <= old.Header.Timestamp {
		b.Header.Timestamp = old.Header.Timestamp + 1
	}
	for _, t := range txs {
		if e := p.engine.ValidateTransaction(ctx, t); e != nil {
			return nil, e
		}
		raw, e := p.engine.EncodeTransaction(t)
		if e != nil {
			return nil, e
		}
		b.Transactions = append(b.Transactions, bytes.Clone(raw))
	}
	b.Header.TransactionsRoot = transactionRoot(b.Transactions)
	b.Header.Consensus, e = p.rule.Draft(ctx, old, b.Header)
	if e != nil {
		return nil, e
	}
	return Wrap(b)
}
func (p *Protocol) Finalize(ctx context.Context, d core.Block, r core.ExecutionResult) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	b, e := Unwrap(d)
	if e != nil {
		return nil, e
	}
	if r.BlockID != d.ID() || r.Parent != core.ID(string(b.Header.Parent)) || len(r.StateRoot) != 32 {
		return nil, fmt.Errorf("execution binding mismatch")
	}
	b.Header.StateRoot = []byte(r.StateRoot)
	b.Header.ReceiptsRoot = receiptRoot(r.Receipts)
	return Wrap(b)
}
func (p *Protocol) ValidateCandidate(ctx context.Context, _ core.ChainStore, parent, b core.Block) error {
	return p.common(ctx, parent, b)
}
func (p *Protocol) common(ctx context.Context, parent, b core.Block) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	old, e := Unwrap(parent)
	if e != nil {
		return e
	}
	v, e := Unwrap(b)
	if e != nil {
		return e
	}
	if old.Header.Height == math.MaxUint64 || v.Header.Height != old.Header.Height+1 || core.ID(string(v.Header.Parent)) != parent.ID() {
		return core.ErrConflict
	}
	if v.Header.Version != 1 || !bytes.Equal(v.Header.Binding, p.binding) || !bytes.Equal(old.Header.Binding, p.binding) || !bytes.Equal(transactionRoot(v.Transactions), v.Header.TransactionsRoot) {
		return fmt.Errorf("invalid composed header")
	}
	txs, e := p.Transactions(b)
	if e != nil {
		return e
	}
	for _, t := range txs {
		if e = p.engine.ValidateTransaction(ctx, t); e != nil {
			return e
		}
	}
	return nil
}
func (p *Protocol) validate(ctx context.Context, chain core.ChainStore, parent, b core.Block, r core.ExecutionResult, sealed bool) error {
	if e := p.common(ctx, parent, b); e != nil {
		return e
	}
	old, _ := Unwrap(parent)
	v, _ := Unwrap(b)
	if r.Parent != parent.ID() || r.BlockID != b.ID() || !bytes.Equal(v.Header.StateRoot, []byte(r.StateRoot)) || !bytes.Equal(v.Header.ReceiptsRoot, receiptRoot(r.Receipts)) {
		return fmt.Errorf("execution commitment mismatch")
	}
	return p.rule.Validate(ctx, chain, old, v, sealed)
}
func (p *Protocol) ValidateBlock(ctx context.Context, chain core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	return p.validate(ctx, chain, parent, b, r, true)
}
func (p *Protocol) ValidateProposal(ctx context.Context, chain core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	return p.validate(ctx, chain, parent, b, r, false)
}
func (p *Protocol) StateCommitment(ctx context.Context, state map[string][]byte) (core.Commitment, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	return core.Commitment(string(stateHash(state))), nil
}
func stateHash(state map[string][]byte) []byte {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := []byte("gochainkit/composed/state/v1\x00")
	for _, k := range keys {
		data = binary.BigEndian.AppendUint64(data, uint64(len(k)))
		data = append(data, []byte(k)...)
		data = binary.BigEndian.AppendUint64(data, uint64(len(state[k])))
		data = append(data, state[k]...)
	}
	return DoubleHash(data)
}
func transactionRoot(txs [][]byte) []byte {
	data := []byte("gochainkit/composed/txs/v1\x00")
	for _, t := range txs {
		data = binary.BigEndian.AppendUint64(data, uint64(len(t)))
		data = append(data, t...)
	}
	return DoubleHash(data)
}
func receiptRoot(rs []core.Receipt) []byte {
	data := []byte("gochainkit/composed/receipts/v1\x00")
	for _, r := range rs {
		data = binary.BigEndian.AppendUint64(data, uint64(len(r.TransactionID)))
		data = append(data, []byte(r.TransactionID)...)
		data = binary.BigEndian.AppendUint64(data, uint64(len(r.Data)))
		data = append(data, r.Data...)
	}
	return DoubleHash(data)
}
func (p *Protocol) Score(ctx context.Context, h core.ChainStore, b core.Block, parent []byte) ([]byte, error) {
	e, err := Unwrap(b)
	if err != nil {
		return nil, err
	}
	return p.choice.Score(ctx, h, e, bytes.Clone(parent))
}
func (p *Protocol) Prefer(ctx context.Context, h core.ChainStore, a, b core.Block, x, y []byte) (bool, error) {
	av, e := Unwrap(a)
	if e != nil {
		return false, e
	}
	bv, e := Unwrap(b)
	if e != nil {
		return false, e
	}
	return p.choice.Prefer(ctx, h, av, bv, bytes.Clone(x), bytes.Clone(y))
}
func (p *Protocol) Finalized(ctx context.Context, h core.ChainStore, b core.Block) (core.ID, error) {
	v, e := Unwrap(b)
	if e != nil {
		return "", e
	}
	return p.choice.Finalized(ctx, h, v)
}

type baseProtocol = Protocol
type WorkProtocol struct {
	*baseProtocol
	work WorkRule
}

func NewPoW(c Config, engine model.Engine, rule WorkRule, choice Choice) (*WorkProtocol, error) {
	p, e := New(c, engine, rule, choice)
	if e != nil {
		return nil, e
	}
	return &WorkProtocol{p, rule}, nil
}
func (p *WorkProtocol) Work(b core.Block) (*big.Int, error) {
	v, e := Unwrap(b)
	if e != nil {
		return nil, e
	}
	return p.work.Work(v)
}
func (p *WorkProtocol) Seal(ctx context.Context, b core.Block) (core.Block, error) {
	v, e := Unwrap(b)
	if e != nil {
		return nil, e
	}
	v, e = p.work.Seal(ctx, v)
	if e != nil {
		return nil, e
	}
	return Wrap(v)
}
