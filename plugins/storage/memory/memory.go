// Package memory provides an ephemeral protocol-neutral backend.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/runtime/ledger"
	"github.com/lengzhao/pluginkit"
	"sync"
)

type Config struct{}
type Deps struct {
	Protocol contracts.Protocol `json:"protocol"`
}
type Store struct {
	mu           sync.RWMutex
	p            contracts.Protocol
	head         contracts.ID
	blocks       map[contracts.ID][]byte
	states       map[contracts.ID]map[string][]byte
	roots        map[contracts.ID]contracts.Commitment
	transactions map[contracts.ID]ledger.Record
	receipts     map[contracts.ID]contracts.Receipt
}

func init() { pluginkit.Register("storage/memory", New) }
func New(_ Config, d Deps) (*Store, error) {
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
	return &Store{p: d.Protocol, head: g.ID, blocks: map[contracts.ID][]byte{g.ID: g.Block}, states: map[contracts.ID]map[string][]byte{g.ID: g.State}, roots: map[contracts.ID]contracts.Commitment{g.ID: g.Root}, transactions: map[contracts.ID]ledger.Record{}, receipts: map[contracts.ID]contracts.Receipt{}}, nil
}
func (s *Store) Protocol() contracts.ProtocolDescriptor { return s.p.Protocol() }
func (s *Store) Head(ctx context.Context) (contracts.ID, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.head, nil
}
func (s *Store) Block(ctx context.Context, id contracts.ID) (contracts.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	data, ok := s.blocks[id]
	data = bytes.Clone(data)
	s.mu.RUnlock()
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return ledger.DecodeBlock(s.p, data, id)
}
func (s *Store) Receipt(ctx context.Context, id contracts.ID) (contracts.Receipt, error) {
	if e := ctx.Err(); e != nil {
		return contracts.Receipt{}, e
	}
	s.mu.RLock()
	r, ok := s.receipts[id]
	r.Data = bytes.Clone(r.Data)
	s.mu.RUnlock()
	if !ok {
		return r, contracts.ErrNotFound
	}
	return r, nil
}
func (s *Store) Transaction(ctx context.Context, id contracts.ID) (contracts.ConfirmedTransaction, error) {
	if e := ctx.Err(); e != nil {
		return contracts.ConfirmedTransaction{}, e
	}
	s.mu.RLock()
	r, ok := s.transactions[id]
	r.Data = bytes.Clone(r.Data)
	s.mu.RUnlock()
	if !ok {
		return contracts.ConfirmedTransaction{}, contracts.ErrNotFound
	}
	t, e := ledger.DecodeTransaction(s.p, r.Data, id)
	return contracts.ConfirmedTransaction{Transaction: t, BlockID: r.BlockID, Index: r.Index}, e
}
func (s *Store) Snapshot(ctx context.Context, id contracts.ID) (contracts.StateReader, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[id]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return &ledger.Snapshot{State: ledger.Copy(state), Commitment: s.roots[id]}, nil
}
func (s *Store) Commit(ctx context.Context, parent contracts.ID, b contracts.Block, r contracts.ExecutionResult) error {
	if b == nil {
		return fmt.Errorf("nil block")
	}
	s.mu.RLock()
	_, exists := s.blocks[b.ID()]
	head := s.head
	s.mu.RUnlock()
	if exists {
		return contracts.ErrAlreadyCommitted
	}
	if head != parent {
		return contracts.ErrConflict
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
		return contracts.ErrAlreadyCommitted
	}
	if s.head != parent {
		return contracts.ErrConflict
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
