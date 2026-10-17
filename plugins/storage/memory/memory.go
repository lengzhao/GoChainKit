// Package memory provides an ephemeral chain backend for development. All data
// disappears on process exit; this is not the durable storage implementation.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"github.com/lengzhao/pluginkit"
	"sync"
)

type Config struct {
	ChainID string `json:"chainID"`
}
type Store struct {
	mu       sync.RWMutex
	head     types.Hash
	blocks   map[types.Hash]types.Block
	states   map[types.Hash]map[string][]byte
	receipts map[types.Hash]types.Receipt
}

func init() { pluginkit.Register("storage/memory", New) }
func New(cfg Config) (*Store, error) {
	root, _ := types.TransactionRoot(nil)
	b := types.Block{Header: types.BlockHeader{Version: 1, ChainID: cfg.ChainID, TransactionRoot: root, StateRoot: types.StateRoot(nil), ReceiptRoot: types.ReceiptRoot(nil)}}
	id, err := b.ID()
	if err != nil {
		return nil, err
	}
	return &Store{head: id, blocks: map[types.Hash]types.Block{id: b}, states: map[types.Hash]map[string][]byte{id: {}}, receipts: make(map[types.Hash]types.Receipt)}, nil
}
func copyState(src map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(src))
	for k, v := range src {
		out[k] = bytes.Clone(v)
	}
	return out
}
func (s *Store) Head(ctx context.Context) (types.Hash, error) {
	if err := ctx.Err(); err != nil {
		return types.Hash{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.head, nil
}
func (s *Store) Block(ctx context.Context, id types.Hash) (types.Block, error) {
	if err := ctx.Err(); err != nil {
		return types.Block{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blocks[id]
	if !ok {
		return types.Block{}, contracts.ErrNotFound
	}
	return b.Clone(), nil
}
func (s *Store) Receipt(ctx context.Context, id types.Hash) (types.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return types.Receipt{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.receipts[id]
	if !ok {
		return types.Receipt{}, contracts.ErrNotFound
	}
	return r, nil
}
func (s *Store) Snapshot(ctx context.Context, id types.Hash) (contracts.StateReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[id]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return &snapshot{state: copyState(state)}, nil
}
func (s *Store) Commit(ctx context.Context, parent types.Hash, b types.Block, result types.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b = b.Clone()
	id, err := b.ID()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := s.blocks[id]; ok {
		return contracts.ErrAlreadyCommitted
	}
	if parent != s.head || b.Header.Parent != parent {
		return contracts.ErrConflict
	}
	previous := s.blocks[parent]
	if previous.Header.Height == ^uint64(0) || b.Header.Height != previous.Header.Height+1 || b.Header.ChainID != previous.Header.ChainID {
		return fmt.Errorf("invalid ancestry")
	}
	if result.Parent != parent || result.BlockID != id || result.StateRoot != b.Header.StateRoot || types.ReceiptRoot(result.Receipts) != b.Header.ReceiptRoot || len(result.Receipts) != len(b.Transactions) {
		return fmt.Errorf("execution binding mismatch")
	}
	next := copyState(s.states[parent])
	for k, v := range result.Writes {
		next[k] = bytes.Clone(v)
	}
	if types.StateRoot(next) != result.StateRoot {
		return fmt.Errorf("state root mismatch")
	}
	for i, tx := range b.Transactions {
		txID, err := tx.ID()
		if err != nil {
			return err
		}
		if result.Receipts[i].TransactionID != txID {
			return fmt.Errorf("receipt transaction mismatch")
		}
	}
	// No fallible work remains after publication begins. The mutex protects the
	// complete update from concurrent readers; process crashes still lose all data.
	s.states[id] = next
	s.blocks[id] = b
	for _, r := range result.Receipts {
		s.receipts[r.TransactionID] = r
	}
	s.head = id
	return nil
}

type snapshot struct{ state map[string][]byte }

func (s *snapshot) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := s.state[string(key)]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s *snapshot) Entries(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return copyState(s.state), nil
}
func (s *snapshot) Root() types.Hash { return types.StateRoot(s.state) }
func (s *snapshot) Close() error     { return nil }
