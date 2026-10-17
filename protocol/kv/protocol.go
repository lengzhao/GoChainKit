// Package kv provides the reference signed key-value chain protocol.
package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/protocol/kv/types"
	"io"
	"math"
)

type Config struct {
	ChainID string `json:"chainID"`
}
type protocol struct{ network string }
type transaction struct {
	value types.Transaction
	id    contracts.ID
}

func (t *transaction) ID() contracts.ID { return t.id }

type block struct {
	value types.Block
	id    contracts.ID
}

func (b *block) ID() contracts.ID { return b.id }
func WrapTransaction(t types.Transaction) (contracts.Transaction, error) {
	if e := t.Verify(t.ChainID); e != nil {
		return nil, e
	}
	id, e := t.ID()
	if e != nil {
		return nil, e
	}
	return &transaction{t.Clone(), contracts.ID(string(id[:]))}, nil
}
func UnwrapTransaction(t contracts.Transaction) (types.Transaction, error) {
	v, ok := t.(*transaction)
	if !ok || v == nil {
		return types.Transaction{}, fmt.Errorf("not a kv transaction")
	}
	return v.value.Clone(), nil
}
func WrapBlock(b types.Block) (contracts.Block, error) {
	id, e := b.ID()
	if e != nil {
		return nil, e
	}
	for _, t := range b.Transactions {
		if e = t.Verify(b.Header.ChainID); e != nil {
			return nil, e
		}
	}
	return &block{b.Clone(), contracts.ID(string(id[:]))}, nil
}
func UnwrapBlock(b contracts.Block) (types.Block, error) {
	v, ok := b.(*block)
	if !ok || v == nil {
		return types.Block{}, fmt.Errorf("not a kv block")
	}
	return v.value.Clone(), nil
}
func New(c Config) (contracts.Protocol, error) {
	if c.ChainID == "" || len(c.ChainID) > types.MaxChainIDBytes {
		return nil, fmt.Errorf("invalid chainID")
	}
	return &protocol{c.ChainID}, nil
}
func (p *protocol) Protocol() contracts.ProtocolDescriptor {
	return contracts.ProtocolDescriptor{Name: "kv", Version: "1", Network: p.network}
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
func (p *protocol) EncodeTransaction(t contracts.Transaction) ([]byte, error) {
	v, e := UnwrapTransaction(t)
	if e != nil {
		return nil, e
	}
	if e = v.Verify(p.network); e != nil {
		return nil, e
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeTransaction(data []byte) (contracts.Transaction, error) {
	var t types.Transaction
	if e := strict(data, &t); e != nil {
		return nil, e
	}
	if e := t.Verify(p.network); e != nil {
		return nil, e
	}
	return WrapTransaction(t)
}
func (p *protocol) ValidateTransaction(ctx context.Context, t contracts.Transaction) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	v, e := UnwrapTransaction(t)
	if e != nil {
		return e
	}
	return v.Verify(p.network)
}
func (p *protocol) EncodeBlock(b contracts.Block) ([]byte, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	return json.Marshal(v)
}
func (p *protocol) DecodeBlock(data []byte) (contracts.Block, error) {
	var b types.Block
	if e := strict(data, &b); e != nil {
		return nil, e
	}
	if b.Header.ChainID != p.network {
		return nil, fmt.Errorf("wrong chain")
	}
	return WrapBlock(b)
}
func (p *protocol) Transactions(b contracts.Block) ([]contracts.Transaction, error) {
	v, e := UnwrapBlock(b)
	if e != nil {
		return nil, e
	}
	out := make([]contracts.Transaction, len(v.Transactions))
	for i, t := range v.Transactions {
		out[i], e = WrapTransaction(t)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (p *protocol) StateCommitment(ctx context.Context, s map[string][]byte) (contracts.Commitment, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	h := types.StateRoot(s)
	return contracts.Commitment(string(h[:])), nil
}
func (p *protocol) Genesis(ctx context.Context) (contracts.Block, map[string][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, nil, e
	}
	s := map[string][]byte{}
	r, _ := types.TransactionRoot(nil)
	b, e := WrapBlock(types.Block{Header: types.BlockHeader{Version: 1, ChainID: p.network, TransactionRoot: r, StateRoot: types.StateRoot(s), ReceiptRoot: types.ReceiptRoot(nil)}})
	return b, s, e
}
func (p *protocol) Draft(ctx context.Context, parent contracts.Block, txs []contracts.Transaction) (contracts.Block, error) {
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
	b := types.Block{Header: types.BlockHeader{Version: 1, ChainID: p.network, Height: v.Header.Height + 1, Parent: id}}
	for _, t := range txs {
		if e = p.ValidateTransaction(ctx, t); e != nil {
			return nil, e
		}
		x, _ := UnwrapTransaction(t)
		b.Transactions = append(b.Transactions, x)
	}
	b.Header.TransactionRoot, e = types.TransactionRoot(b.Transactions)
	if e != nil {
		return nil, e
	}
	return WrapBlock(b)
}
func receiptRoot(rs []contracts.Receipt) (types.Hash, error) {
	out := make([]types.Receipt, len(rs))
	for i, r := range rs {
		if len(r.TransactionID) != 32 || !bytes.Equal(r.Data, []byte{1}) {
			return types.Hash{}, fmt.Errorf("invalid kv receipt")
		}
		copy(out[i].TransactionID[:], []byte(r.TransactionID))
		out[i].Success = true
	}
	return types.ReceiptRoot(out), nil
}
func (p *protocol) Finalize(ctx context.Context, d contracts.Block, r contracts.ExecutionResult) (contracts.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, e := UnwrapBlock(d)
	if e != nil {
		return nil, e
	}
	if v.Header.ChainID != p.network || r.Parent != contracts.ID(string(v.Header.Parent[:])) || r.BlockID != d.ID() || len(r.StateRoot) != 32 {
		return nil, fmt.Errorf("execution binding mismatch")
	}
	if len(r.Receipts) != len(v.Transactions) {
		return nil, fmt.Errorf("receipt count")
	}
	for i, t := range v.Transactions {
		id, _ := t.ID()
		if r.Receipts[i].TransactionID != contracts.ID(string(id[:])) {
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
func (p *protocol) ValidateBlock(ctx context.Context, _ contracts.ChainStore, parent, b contracts.Block, r contracts.ExecutionResult) error {
	pv, e := UnwrapBlock(parent)
	if e != nil {
		return e
	}
	bv, e := UnwrapBlock(b)
	if e != nil {
		return e
	}
	if pv.Header.Height == math.MaxUint64 || bv.Header.Height != pv.Header.Height+1 || bv.Header.Version != 1 || bv.Header.ChainID != p.network || pv.Header.ChainID != p.network || contracts.ID(string(bv.Header.Parent[:])) != parent.ID() {
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
