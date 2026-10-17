// Package memory implements a bounded, nonpersistent transaction pool.
package memory

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"sync"
)

type Config struct {
	ChainID         string `json:"chainID"`
	MaxTransactions int    `json:"maxTransactions"`
	MaxBytes        int    `json:"maxBytes"`
}
type entry struct {
	id   types.Hash
	tx   types.Transaction
	size int
}
type pool struct {
	mu      sync.Mutex
	cfg     Config
	entries []entry
	ids     map[types.Hash]bool
	bytes   int
}

func New(cfg Config) (contracts.Mempool, error) {
	if cfg.ChainID == "" || len(cfg.ChainID) > types.MaxChainIDBytes || cfg.MaxTransactions <= 0 || cfg.MaxBytes <= 0 {
		return nil, fmt.Errorf("mempool/memory: chainID and positive capacity limits required")
	}
	return &pool{cfg: cfg, ids: make(map[types.Hash]bool)}, nil
}
func (p *pool) Add(ctx context.Context, tx types.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx = tx.Clone()
	if err := tx.Verify(p.cfg.ChainID); err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrInvalidTransaction, err)
	}
	encoded, err := tx.MarshalBinary()
	if err != nil {
		return err
	}
	id, err := tx.ID()
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.ids[id] {
		return nil
	}
	if len(p.entries) >= p.cfg.MaxTransactions || len(encoded) > p.cfg.MaxBytes-p.bytes {
		return contracts.ErrFull
	}
	p.entries = append(p.entries, entry{id: id, tx: tx, size: len(encoded)})
	p.ids[id] = true
	p.bytes += len(encoded)
	return nil
}
func (p *pool) Select(ctx context.Context, maxBytes int) ([]types.Transaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("negative selection budget")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []types.Transaction
	for _, e := range p.entries {
		if e.size > maxBytes {
			continue
		}
		out = append(out, e.tx.Clone())
		maxBytes -= e.size
	}
	return out, nil
}
func (p *pool) Remove(ctx context.Context, ids []types.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	remove := make(map[types.Hash]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	kept := p.entries[:0]
	for _, e := range p.entries {
		if remove[e.id] {
			delete(p.ids, e.id)
			p.bytes -= e.size
		} else {
			kept = append(kept, e)
		}
	}
	clear(p.entries[len(kept):])
	p.entries = kept
	return nil
}
