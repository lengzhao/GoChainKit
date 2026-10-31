// Package materializer assembles complete ordered bodies from bounded sources.
package materializer

import (
	"bytes"
	"context"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	"github.com/lengzhao/GoChainKit/core"
	"time"
)

type Config struct {
	Timeout       string `json:"timeout"`
	MaxReferences int    `json:"maxReferences"`
	MaxBytes      int    `json:"maxBytes"`
	MaxConcurrent int    `json:"maxConcurrent"`
}
type Deps struct {
	References bodycap.References          `json:"references"`
	Protocol   core.TransactionProtocol    `json:"protocol"`
	Sources    []bodycap.TransactionSource `json:"sources"`
}
type Materializer struct {
	cfg     Config
	d       Deps
	slots   chan struct{}
	timeout time.Duration
}

func New(c Config, d Deps) (*Materializer, error) {
	if d.References == nil || d.Protocol == nil || len(d.Sources) == 0 {
		return nil, fmt.Errorf("body: references, protocol and sources required")
	}
	if c.MaxReferences == 0 {
		c.MaxReferences = 4096
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 4 << 20
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 8
	}
	if c.MaxReferences < 1 || c.MaxReferences > 65536 || c.MaxBytes > 64<<20 || c.MaxBytes < 1 || c.MaxConcurrent < 1 || c.MaxConcurrent > 4096 {
		return nil, fmt.Errorf("body: invalid budget")
	}
	if e := core.RequireCompatible(d.Protocol.Protocol(), d.References); e != nil {
		return nil, e
	}
	for _, s := range d.Sources {
		if s == nil {
			return nil, fmt.Errorf("body: nil source")
		}
		if e := core.RequireCompatible(d.Protocol.Protocol(), s); e != nil {
			return nil, e
		}
	}
	d.Sources = append([]bodycap.TransactionSource(nil), d.Sources...)
	timeout := 30 * time.Second
	if c.Timeout != "" {
		var e error
		timeout, e = time.ParseDuration(c.Timeout)
		if e != nil || timeout <= 0 {
			return nil, fmt.Errorf("body: positive timeout required")
		}
	}
	return &Materializer{cfg: c, d: d, slots: make(chan struct{}, c.MaxConcurrent), timeout: timeout}, nil
}
func (m *Materializer) Protocol() core.ProtocolDescriptor { return m.d.Protocol.Protocol() }

// Only a pure missing-record outcome allows fallback. Mixed failures are preserved.
func missing(e error) bool {
	if e == core.ErrNotFound {
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return missing(w.Unwrap())
	}
	return false
}
func (m *Materializer) Materialize(ctx context.Context, b core.Block) (core.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if b == nil {
		return nil, fmt.Errorf("body: nil block")
	}
	refs, e := m.d.References.References(b)
	if e != nil {
		return nil, e
	}
	if len(refs) > m.cfg.MaxReferences {
		return nil, core.ErrFull
	}
	out := make([]core.Transaction, len(refs))
	used := 0
	for i, id := range refs {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if id == "" {
			return nil, fmt.Errorf("body: empty reference")
		}
		var tx core.Transaction
		for _, s := range m.d.Sources {
			tx, e = s.Transaction(ctx, id)
			if e == nil {
				break
			}
			if !missing(e) {
				return nil, e
			}
		}
		if e != nil {
			return nil, e
		}
		if tx == nil || tx.ID() != id {
			return nil, fmt.Errorf("body: transaction identity mismatch")
		}
		if e = m.d.Protocol.ValidateTransaction(ctx, tx); e != nil {
			return nil, e
		}
		raw, e := m.d.Protocol.EncodeTransaction(tx)
		if e != nil {
			return nil, e
		}
		if len(raw) > m.cfg.MaxBytes-used {
			return nil, core.ErrFull
		}
		used += len(raw)
		frozen, e := m.d.Protocol.DecodeTransaction(bytes.Clone(raw))
		if e != nil {
			return nil, e
		}
		if frozen == nil || frozen.ID() != id {
			return nil, fmt.Errorf("body: codec identity mismatch")
		}
		again, e := m.d.Protocol.EncodeTransaction(frozen)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(raw, again) {
			return nil, fmt.Errorf("body: noncanonical transaction")
		}
		out[i] = frozen
	}
	warm, e := m.d.References.Attach(b, out)
	if e != nil {
		return nil, e
	}
	if warm == nil || warm.ID() != b.ID() {
		return nil, fmt.Errorf("body: hydration changed identity")
	}
	return warm, nil
}
