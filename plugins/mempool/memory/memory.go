// Package memory implements a format-independent bounded transaction pool.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"sync"
)

type Config struct {
	MaxTransactions int `json:"maxTransactions"`
	MaxBytes        int `json:"maxBytes"`
}
type Deps struct {
	Protocol contracts.Protocol `json:"protocol"`
}
type entry struct {
	id   contracts.ID
	data []byte
}
type pool struct {
	mu       sync.Mutex
	cfg      Config
	protocol contracts.Protocol
	entries  []entry
	ids      map[contracts.ID]bool
	bytes    int
}

func New(cfg Config, deps Deps) (contracts.Mempool, error) {
	if deps.Protocol == nil || cfg.MaxTransactions <= 0 || cfg.MaxBytes <= 0 {
		return nil, fmt.Errorf("mempool/memory: protocol and positive limits required")
	}
	if err := deps.Protocol.Protocol().Validate(); err != nil {
		return nil, err
	}
	return &pool{cfg: cfg, protocol: deps.Protocol, ids: make(map[contracts.ID]bool)}, nil
}
func (p *pool) Protocol() contracts.ProtocolDescriptor { return p.protocol.Protocol() }
func (p *pool) Add(ctx context.Context, t contracts.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.ID() == "" {
		return contracts.ErrInvalidTransaction
	}
	if err := p.protocol.ValidateTransaction(ctx, t); err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrInvalidTransaction, err)
	}
	data, err := p.protocol.EncodeTransaction(t)
	if err != nil {
		return err
	}
	owned, err := p.protocol.DecodeTransaction(data)
	if err != nil {
		return err
	}
	if owned == nil || owned.ID() != t.ID() {
		return fmt.Errorf("transaction codec changed identity")
	}
	id := owned.ID()
	data = bytes.Clone(data)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.ids[id] {
		return nil
	}
	if len(p.entries) >= p.cfg.MaxTransactions || len(data) > p.cfg.MaxBytes-p.bytes {
		return contracts.ErrFull
	}
	p.entries = append(p.entries, entry{id, data})
	p.ids[id] = true
	p.bytes += len(data)
	return nil
}
func (p *pool) Select(ctx context.Context, budget int) ([]contracts.Transaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if budget < 0 {
		return nil, fmt.Errorf("negative selection budget")
	}
	p.mu.Lock()
	var selected [][]byte
	for _, e := range p.entries {
		if len(e.data) <= budget {
			selected = append(selected, bytes.Clone(e.data))
			budget -= len(e.data)
		}
	}
	p.mu.Unlock()
	var out []contracts.Transaction
	for _, data := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, err := p.protocol.DecodeTransaction(data)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
func (p *pool) Remove(ctx context.Context, ids []contracts.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	remove := make(map[contracts.ID]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	kept := p.entries[:0]
	for _, e := range p.entries {
		if remove[e.id] {
			delete(p.ids, e.id)
			p.bytes -= len(e.data)
		} else {
			kept = append(kept, e)
		}
	}
	clear(p.entries[len(kept):])
	p.entries = kept
	return nil
}
