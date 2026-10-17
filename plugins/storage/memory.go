// Package memory provides an ephemeral protocol-neutral backend.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/runtime/ledger"
	"github.com/lengzhao/pluginkit"
	"sync"
)

type MemoryConfig struct{}
type Deps struct {
	Protocol core.Protocol `json:"protocol"`
}
type MemoryStore struct {
	mu           sync.RWMutex
	p            core.Protocol
	head         core.ID
	blocks       map[core.ID][]byte
	states       map[core.ID]map[string][]byte
	roots        map[core.ID]core.Commitment
	transactions map[core.ID]ledger.Record
	receipts     map[core.ID]core.Receipt
}

func init() { pluginkit.Register("storage/memory", NewMemory) }
func NewMemory(_ MemoryConfig, d Deps) (*MemoryStore, error) {
	if d.Protocol == nil {
		return nil, fmt.Errorf("protocol required")
	}
	if e := d.Protocol.Protocol().Validate(); e != nil {
		return nil, e
	}
	g, e := ledger.Genesis(context.Background(), d.Protocol)
	if e != nil {
		return nil, e
	}
	return &MemoryStore{p: d.Protocol, head: g.ID, blocks: map[core.ID][]byte{g.ID: g.Block}, states: map[core.ID]map[string][]byte{g.ID: g.State}, roots: map[core.ID]core.Commitment{g.ID: g.Root}, transactions: map[core.ID]ledger.Record{}, receipts: map[core.ID]core.Receipt{}}, nil
}
func (s *MemoryStore) Protocol() core.ProtocolDescriptor { return s.p.Protocol() }
func (s *MemoryStore) Head(ctx context.Context) (core.ID, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.head, nil
}
func (s *MemoryStore) Block(ctx context.Context, id core.ID) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	data, ok := s.blocks[id]
	data = bytes.Clone(data)
	s.mu.RUnlock()
	if !ok {
		return nil, core.ErrNotFound
	}
	return ledger.DecodeBlock(s.p, data, id)
}
func (s *MemoryStore) Receipt(ctx context.Context, id core.ID) (core.Receipt, error) {
	if e := ctx.Err(); e != nil {
		return core.Receipt{}, e
	}
	s.mu.RLock()
	r, ok := s.receipts[id]
	r.Data = bytes.Clone(r.Data)
	s.mu.RUnlock()
	if !ok {
		return r, core.ErrNotFound
	}
	return r, nil
}
func (s *MemoryStore) Transaction(ctx context.Context, id core.ID) (core.ConfirmedTransaction, error) {
	if e := ctx.Err(); e != nil {
		return core.ConfirmedTransaction{}, e
	}
	s.mu.RLock()
	r, ok := s.transactions[id]
	r.Data = bytes.Clone(r.Data)
	s.mu.RUnlock()
	if !ok {
		return core.ConfirmedTransaction{}, core.ErrNotFound
	}
	t, e := ledger.DecodeTransaction(s.p, r.Data, id)
	return core.ConfirmedTransaction{Transaction: t, BlockID: r.BlockID, Index: r.Index}, e
}
func (s *MemoryStore) Snapshot(ctx context.Context, id core.ID) (core.StateReader, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return &ledger.Snapshot{State: ledger.Copy(state), Commitment: s.roots[id]}, nil
}
func (s *MemoryStore) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	if b == nil {
		return fmt.Errorf("nil block")
	}
	s.mu.RLock()
	_, exists := s.blocks[b.ID()]
	head := s.head
	s.mu.RUnlock()
	if exists {
		return core.ErrAlreadyCommitted
	}
	if head != parent {
		return core.ErrConflict
	}
	v, e := ledger.Prepare(ctx, s.p, s, parent, b, r)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = ctx.Err(); e != nil {
		return e
	}
	if _, ok := s.blocks[v.ID]; ok {
		return core.ErrAlreadyCommitted
	}
	if s.head != parent {
		return core.ErrConflict
	}
	for id := range v.Transactions {
		if _, ok := s.transactions[id]; ok {
			return fmt.Errorf("transaction already confirmed")
		}
	}
	s.blocks[v.ID] = v.Block
	s.states[v.ID] = v.State
	s.roots[v.ID] = v.Root
	for id, t := range v.Transactions {
		s.transactions[id] = t
	}
	for id, r := range v.Receipts {
		s.receipts[id] = r
	}
	s.head = v.ID
	return nil
}
