// Storage helpers keep preparation and isolated snapshots private to backends.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
)

type storedTransaction struct {
	Data    []byte
	BlockID core.ID
	Index   uint64
}
type preparedCommit struct {
	Result       core.ExecutionResult
	ID           core.ID
	Block        []byte
	State        map[string][]byte
	Root         core.Commitment
	Transactions map[core.ID]storedTransaction
	Receipts     map[core.ID]core.Receipt
}

func copyState(s map[string][]byte) map[string][]byte {
	o := make(map[string][]byte, len(s))
	for k, v := range s {
		o[k] = bytes.Clone(v)
	}
	return o
}
func decodeStoredBlock(p core.LedgerProtocol, data []byte, id core.ID) (core.Block, error) {
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
func decodeStoredTransaction(p core.LedgerProtocol, data []byte, id core.ID) (core.Transaction, error) {
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
func prepareGenesis(ctx context.Context, p core.LedgerProtocol, access ...*bodyAccess) (preparedCommit, error) {
	b, s, e := p.Genesis(ctx)
	if e != nil {
		return preparedCommit{}, e
	}
	if b == nil || b.ID() == "" {
		return preparedCommit{}, fmt.Errorf("empty genesis")
	}
	data, e := p.EncodeBlock(b)
	if e != nil {
		return preparedCommit{}, e
	}
	if _, e = decodeAvailableBlock(ctx, p, data, b.ID(), access...); e != nil {
		return preparedCommit{}, e
	}
	root, e := p.StateCommitment(ctx, s)
	return preparedCommit{ID: b.ID(), Block: bytes.Clone(data), State: copyState(s), Root: root}, e
}
func prepareCommit(ctx context.Context, p core.LedgerProtocol, chain core.LedgerReader, parent core.ID, b core.Block, r core.ExecutionResult, access ...*bodyAccess) (preparedCommit, error) {
	bad := func(e error) (preparedCommit, error) { return preparedCommit{}, e }
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
	snap, e := chain.Snapshot(ctx, parent)
	if e != nil {
		return bad(e)
	}
	state, e := snap.Entries(ctx)
	snap.Close()
	if e != nil {
		return bad(e)
	}
	state, e = core.ApplyStateChanges(ctx, state, r.Writes, r.Deletes)
	if e != nil {
		return bad(e)
	}
	root, e := p.StateCommitment(ctx, state)
	if e != nil {
		return bad(e)
	}
	if root != r.StateRoot {
		return bad(fmt.Errorf("state root mismatch"))
	}
	if a := firstBody(access); a != nil {
		if e = p.ValidateBlock(ctx, chain, previous, b, r); e != nil {
			return bad(e)
		}
		txs, e := p.Transactions(b)
		if e != nil {
			return bad(e)
		}
		for _, tx := range txs {
			if e = a.archive.Put(ctx, tx); e != nil {
				return bad(e)
			}
		}
	}
	data, e := p.EncodeBlock(b)
	if e != nil {
		return bad(e)
	}
	b, e = decodeAvailableBlock(ctx, p, data, b.ID(), access...)
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
	out := preparedCommit{ID: b.ID(), Block: bytes.Clone(data), State: copyState(state), Root: root, Transactions: map[core.ID]storedTransaction{}, Receipts: map[core.ID]core.Receipt{}}
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
		if _, e = decodeStoredTransaction(p, encoded, t.ID()); e != nil {
			return bad(e)
		}
		out.Transactions[t.ID()] = storedTransaction{bytes.Clone(encoded), b.ID(), uint64(i)}
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
	out.Result = core.ExecutionResult{Parent: r.Parent, BlockID: r.BlockID, StateRoot: r.StateRoot, Writes: copyState(r.Writes), Deletes: append([]string(nil), r.Deletes...)}
	// Preserve protocol receipt ordering.
	out.Result.Receipts = nil
	for _, receipt := range r.Receipts {
		receipt.Data = bytes.Clone(receipt.Data)
		out.Result.Receipts = append(out.Result.Receipts, receipt)
	}
	return out, nil
}

type stateSnapshot struct {
	State      map[string][]byte
	Commitment core.Commitment
}

func (s *stateSnapshot) Get(ctx context.Context, key []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	v, ok := s.State[string(key)]
	if !ok {
		return nil, core.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s *stateSnapshot) Entries(ctx context.Context) (map[string][]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return copyState(s.State), nil
}
func (s *stateSnapshot) Root() core.Commitment { return s.Commitment }
func (s *stateSnapshot) Close() error          { return nil }
