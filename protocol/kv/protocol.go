// Package kv provides the reference signed key-value chain protocol.
package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"io"
	"math"
)

type Config struct {
	ChainID string `json:"chainID"`
}
type protocol struct{ network string }
type transaction struct {
	value Transaction
	id    core.ID
}

func (t *transaction) ID() core.ID { return t.id }

type block struct {
	value Block
	id    core.ID
}

func (b *block) ID() core.ID { return b.id }
func WrapTransaction(t Transaction) (core.Transaction, error) {
	if e := t.Verify(t.ChainID); e != nil {
		return nil, e
	}
	id, e := t.ID()
	if e != nil {
		return nil, e
	}
	return &transaction{t.Clone(), core.ID(string(id[:]))}, nil
}
func UnwrapTransaction(t core.Transaction) (Transaction, error) {
	v, ok := t.(*transaction)
	if !ok || v == nil {
		return Transaction{}, fmt.Errorf("not a kv transaction")
	}
	return v.value.Clone(), nil
}
func WrapBlock(b Block) (core.Block, error) {
	id, e := b.ID()
	if e != nil {
		return nil, e
	}
	for _, t := range b.Transactions {
		if e = t.Verify(b.Header.ChainID); e != nil {
			return nil, e
		}
	}
	return &block{b.Clone(), core.ID(string(id[:]))}, nil
}
func UnwrapBlock(b core.Block) (Block, error) {
	v, ok := b.(*block)
	if !ok || v == nil {
		return Block{}, fmt.Errorf("not a kv block")
	}
	return v.value.Clone(), nil
}
func New(c Config) (core.Protocol, error) {
	if c.ChainID == "" || len(c.ChainID) > MaxChainIDBytes {
		return nil, fmt.Errorf("invalid chainID")
	}
	return &protocol{c.ChainID}, nil
}
func (p *protocol) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "kv", Version: "1", Network: p.network}
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var tail any
	if e := d.Decode(&tail); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func (p *protocol) EncodeTransaction(t core.Transaction) ([]byte, error) {
	v, e := UnwrapTransaction(t)
	if e != nil {
		return nil, e
	}
	if e = v.Verify(p.network); e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeTransaction(data []byte) (core.Transaction, error) {
	var t Transaction
	if e := strict(data, &t); e != nil {
		return nil, e
	}
	if e := t.Verify(p.network); e != nil {
		return nil, e
	}
	return WrapTransaction(t)
}
func (p *protocol) ValidateTransaction(ctx context.Context, t core.Transaction) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	v, e := UnwrapTransaction(t)
	if e != nil {
		return e
	}
	return v.Verify(p.network)
}
func (p *protocol) EncodeBlock(b core.Block) ([]byte, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeBlock(data []byte) (core.Block, error) {
	var b Block
	if e := strict(data, &b); e != nil {
		return nil, e
	}
	if b.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	return WrapBlock(b)
}
func (p *protocol) Transactions(b core.Block) ([]core.Transaction, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	out := make([]core.Transaction, len(v.Transactions))
	for i, t := range v.Transactions {
		out[i], e = WrapTransaction(t)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *protocol) StateCommitment(ctx context.Context, s map[string][]byte) (core.Commitment, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	h := StateRoot(s)
	return core.Commitment(string(h[:])), nil
}
func (p *protocol) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	s := map[string][]byte{}
	r, _ := TransactionRoot(nil)
	b, e := WrapBlock(Block{Header: BlockHeader{Version: 1, ChainID: p.network, TransactionRoot: r, StateRoot: StateRoot(s), ReceiptRoot: ReceiptRoot(nil)}})
	return b, s, e
}
func (p *protocol) Draft(ctx context.Context, parent core.Block, txs []core.Transaction) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := UnwrapBlock(parent)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network || v.Header.Height == math.MaxUint64 {
		return nil, fmt.Errorf("invalid parent")
	}
	id, _ := v.ID()
	b := Block{Header: BlockHeader{Version: 1, ChainID: p.network, Height: v.Header.Height + 1, Parent: id}}
	for _, t := range txs {
		if e = p.ValidateTransaction(ctx, t); e != nil {
			return nil, e
		}
		x, _ := UnwrapTransaction(t)
		b.Transactions = append(b.Transactions, x)
	}
	b.Header.TransactionRoot, e = TransactionRoot(b.Transactions)
	if e != nil {
		return nil, e
	}
	return WrapBlock(b)
}
func receiptRoot(rs []core.Receipt) (Hash, error) {
	out := make([]Receipt, len(rs))
	for i, r := range rs {
		if len(r.TransactionID) != 32 || !bytes.Equal(r.Data, []byte{1}) {
			return Hash{}, fmt.Errorf("invalid kv receipt")
		}
		copy(out[i].TransactionID[:], []byte(r.TransactionID))
		out[i].Success = true
	}
	return ReceiptRoot(out), nil
}
func (p *protocol) Finalize(ctx context.Context, d core.Block, r core.ExecutionResult) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := UnwrapBlock(d)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network || r.Parent != core.ID(string(v.Header.Parent[:])) || r.BlockID != d.ID() || len(r.StateRoot) != 32 {
		return nil, fmt.Errorf("execution binding mismatch")
	}
	if len(r.Receipts) != len(v.Transactions) {
		return nil, fmt.Errorf("receipt count")
	}
	for i, t := range v.Transactions {
		id, _ := t.ID()
		if r.Receipts[i].TransactionID != core.ID(string(id[:])) {
			return nil, fmt.Errorf("receipt transaction mismatch")
		}
	}
	copy(v.Header.StateRoot[:], []byte(r.StateRoot))
	v.Header.ReceiptRoot, e = receiptRoot(r.Receipts)
	if e != nil {
		return nil, e
	}
	return WrapBlock(v)
}
func (p *protocol) ValidateBlock(ctx context.Context, _ core.ChainStore, parent, b core.Block, r core.ExecutionResult) error {
	pv, e := UnwrapBlock(parent)
	if e != nil {
		return e
	}
	bv, e := UnwrapBlock(b)
	if e != nil {
		return e
	}
	if pv.Header.Height == math.MaxUint64 || bv.Header.Height != pv.Header.Height+1 || bv.Header.Version != 1 || bv.Header.ChainID != p.network || pv.Header.ChainID != p.network || core.ID(string(bv.Header.Parent[:])) != parent.ID() {
		return fmt.Errorf("invalid ancestry")
	}
	transactions, e := p.Transactions(b)
	if e != nil {
		return e
	}
	draft, e := p.Draft(ctx, parent, transactions)
	if e != nil {
		return e
	}
	if r.BlockID != b.ID() {
		return fmt.Errorf("result block binding")
	}
	r.BlockID = draft.ID()
	final, e := p.Finalize(ctx, draft, r)
	if e != nil {
		return e
	}
	if final.ID() != b.ID() {
		return fmt.Errorf("commitment mismatch")
	}
	return nil
}
