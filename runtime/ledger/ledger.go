// Package ledger holds protocol-neutral storage preparation helpers.
package ledger

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
)

type Record struct {
	Data    []byte
	BlockID contracts.ID
	Index   uint64
}
type Prepared struct {
	Result       contracts.ExecutionResult
	ID           contracts.ID
	Block        []byte
	State        map[string][]byte
	Root         contracts.Commitment
	Transactions map[contracts.ID]Record
	Receipts     map[contracts.ID]contracts.Receipt
}

func Copy(s map[string][]byte) map[string][]byte {
	o := make(map[string][]byte, len(s))
	for k, v := range s {
		o[k] = bytes.Clone(v)
	}
	return o
}
func DecodeBlock(p contracts.Protocol, data []byte, id contracts.ID) (contracts.Block, error) {
	b, e := p.DecodeBlock(bytes.Clone(data))
	if e != nil {
		return nil, e
	}
	if b == nil || b.ID() == "" || b.ID() != id {
		return nil, fmt.Errorf("block identity mismatch")
	}
	encoded, e := p.EncodeBlock(b)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(encoded, data) {
		return nil, fmt.Errorf("noncanonical block encoding")
	}
	return b, nil
}
func DecodeTransaction(p contracts.Protocol, data []byte, id contracts.ID) (contracts.Transaction, error) {
	t, e := p.DecodeTransaction(bytes.Clone(data))
	if e != nil {
		return nil, e
	}
	if t == nil || t.ID() == "" || t.ID() != id {
		return nil, fmt.Errorf("transaction identity mismatch")
	}
	encoded, e := p.EncodeTransaction(t)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(encoded, data) {
		return nil, fmt.Errorf("noncanonical transaction encoding")
	}
	return t, nil
}
func Genesis(ctx context.Context, p contracts.Protocol) (Prepared, error) {
	b, s, e := p.Genesis(ctx)
	if e != nil {
		return Prepared{}, e
	}
	if b == nil || b.ID() == "" {
		return Prepared{}, fmt.Errorf("empty genesis")
	}
	data, e := p.EncodeBlock(b)
	if e != nil {
		return Prepared{}, e
	}
	if _, e = DecodeBlock(p, data, b.ID()); e != nil {
		return Prepared{}, e
	}
	root, e := p.StateCommitment(ctx, s)
	return Prepared{ID: b.ID(), Block: bytes.Clone(data), State: Copy(s), Root: root}, e
}
func Prepare(ctx context.Context, p contracts.Protocol, chain contracts.ChainStore, parent contracts.ID, b contracts.Block, r contracts.ExecutionResult) (Prepared, error) {
	bad := func(e error) (Prepared, error) { return Prepared{}, e }
	if e := ctx.Err(); e != nil {
		return bad(e)
	}
	if b == nil || b.ID() == "" || r.Parent != parent || r.BlockID != b.ID() {
		return bad(fmt.Errorf("execution binding mismatch"))
	}
	previous, e := chain.Block(ctx, parent)
	if e != nil {
		return bad(e)
	}
	ss, ok := chain.(contracts.StateStore)
	if !ok {
		return bad(fmt.Errorf("missing state store"))
	}
	snap, e := ss.Snapshot(ctx, parent)
	if e != nil {
		return bad(e)
	}
	state, e := snap.Entries(ctx)
	snap.Close()
	if e != nil {
		return bad(e)
	}
	for k, v := range r.Writes {
		state[k] = bytes.Clone(v)
	}
	root, e := p.StateCommitment(ctx, state)
	if e != nil {
		return bad(e)
	}
	if root != r.StateRoot {
		return bad(fmt.Errorf("state root mismatch"))
	}
	data, e := p.EncodeBlock(b)
	if e != nil {
		return bad(e)
	}
	b, e = DecodeBlock(p, data, b.ID())
	if e != nil {
		return bad(e)
	}
	if e = p.ValidateBlock(ctx, chain, previous, b, r); e != nil {
		return bad(e)
	}
	txs, e := p.Transactions(b)
	if e != nil {
		return bad(e)
	}
	out := Prepared{ID: b.ID(), Block: bytes.Clone(data), State: Copy(state), Root: root, Transactions: map[contracts.ID]Record{}, Receipts: map[contracts.ID]contracts.Receipt{}}
	for i, t := range txs {
		if t == nil || t.ID() == "" {
			return bad(fmt.Errorf("invalid transaction"))
		}
		if _, exists := out.Transactions[t.ID()]; exists {
			return bad(fmt.Errorf("duplicate transaction"))
		}
		encoded, e := p.EncodeTransaction(t)
		if e != nil {
			return bad(e)
		}
		if _, e = DecodeTransaction(p, encoded, t.ID()); e != nil {
			return bad(e)
		}
		out.Transactions[t.ID()] = Record{bytes.Clone(encoded), b.ID(), uint64(i)}
	}
	for _, receipt := range r.Receipts {
		if _, ok := out.Transactions[receipt.TransactionID]; !ok {
			return bad(fmt.Errorf("receipt references absent transaction"))
		}
		if _, ok := out.Receipts[receipt.TransactionID]; ok {
			return bad(fmt.Errorf("duplicate receipt"))
		}
		receipt.Data = bytes.Clone(receipt.Data)
		out.Receipts[receipt.TransactionID] = receipt
	}
	out.Result = contracts.ExecutionResult{Parent: r.Parent, BlockID: r.BlockID, StateRoot: r.StateRoot, Writes: Copy(r.Writes)}
	// Preserve protocol receipt ordering.
	out.Result.Receipts = nil
	for _, receipt := range r.Receipts {
		receipt.Data = bytes.Clone(receipt.Data)
		out.Result.Receipts = append(out.Result.Receipts, receipt)
	}
	return out, nil
}

type Snapshot struct {
	State      map[string][]byte
	Commitment contracts.Commitment
}

func (s *Snapshot) Get(ctx context.Context, key []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, ok := s.State[string(key)]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s *Snapshot) Entries(ctx context.Context) (map[string][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return Copy(s.State), nil
}
func (s *Snapshot) Root() contracts.Commitment { return s.Commitment }
func (s *Snapshot) Close() error               { return nil }
