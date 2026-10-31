// Package memory provides an ephemeral protocol-neutral backend.
package storage

import (
	"bytes"
	"context"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"sync"
)

type MemoryConfig struct{}
type Deps struct {
	Archive      bodycap.TransactionArchive `json:"archive,omitempty"`
	Materializer bodycap.Materializer       `json:"materializer,omitempty"`
	Protocol     core.LedgerProtocol        `json:"protocol"`
	Policy       chaincap.BranchPolicy      `json:"policy,omitempty"`
}
type MemoryStore struct {
	body         *bodyAccess
	mu           sync.RWMutex
	owner        *core.LedgerIdentity
	p            core.LedgerProtocol
	policy       chaincap.BranchPolicy
	branches     map[core.ID]branchNode
	head         core.ID
	blocks       map[core.ID][]byte
	states       map[core.ID]map[string][]byte
	roots        map[core.ID]core.Commitment
	transactions map[core.ID]storedTransaction
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
	body, e := newBodyAccess(d.Protocol, d.Archive, d.Materializer)
	if e != nil {
		return nil, e
	}
	policy, e := resolvePolicy(d)
	if e != nil {
		return nil, e
	}
	g, e := prepareGenesis(context.Background(), d.Protocol, body)
	if e != nil {
		return nil, e
	}
	store := &MemoryStore{body: body, owner: core.NewLedgerIdentity(), p: d.Protocol, policy: policy, head: g.ID, blocks: map[core.ID][]byte{g.ID: g.Block}, states: map[core.ID]map[string][]byte{g.ID: g.State}, roots: map[core.ID]core.Commitment{g.ID: g.Root}, transactions: map[core.ID]storedTransaction{}, receipts: map[core.ID]core.Receipt{}}
	if policy != nil {
		p := d.Protocol.(chaincap.BranchProtocol)
		n, e := branchGenesis(context.Background(), p, policy, g, body)
		if e != nil {
			return nil, e
		}
		store.branches = map[core.ID]branchNode{g.ID: n}
	}
	return store, nil
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
	return decodeAvailableBlock(ctx, s.p, data, id, s.body)
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
	t, e := decodeStoredTransaction(s.p, r.Data, id)
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
	return &stateSnapshot{State: copyState(state), Commitment: s.roots[id]}, nil
}
func (s *MemoryStore) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	if s.policy != nil {
		_, e := s.importBlock(ctx, parent, b, r, true)
		return e
	}
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
	v, e := prepareCommit(ctx, s.p, s, parent, b, r, s.body)
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

func (s *MemoryStore) LedgerID() *core.LedgerIdentity { return s.owner }

var _ core.Ledger = (*MemoryStore)(nil)
