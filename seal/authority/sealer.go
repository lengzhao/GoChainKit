// Package authority holds local signing authority separately from consensus validation.
package authority

import (
	"bytes"
	"context"
	"fmt"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/poa"
	"time"
)

type Rule interface {
	Slot(int64) (uint64, error)
	Expected(context.Context, composed.Envelope, composed.Header) (valcap.Member, []byte, error)
}
type Deps struct {
	Rule   Rule           `json:"rule"`
	Signer signcap.Signer `json:"signer"`
}
type Config struct {
	Clock func() time.Time `json:"-"`
}
type Sealer struct {
	deps  Deps
	clock func() time.Time
}

func New(c Config, d Deps) (*Sealer, error) {
	if d.Rule == nil || d.Signer == nil {
		return nil, fmt.Errorf("authority sealer dependencies required")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	return &Sealer{d, c.Clock}, nil
}

// Ready is local scheduling only; historical rule validation never uses the clock.
func (s *Sealer) Ready(ctx context.Context, parent core.Block) error {
	p, e := composed.Unwrap(parent)
	if e != nil {
		return e
	}
	now := s.clock().Unix()
	slot, e := s.deps.Rule.Slot(now)
	if e != nil {
		if _, ok := e.(*poa.ScheduleError); ok {
			return core.ErrNoProposal
		}
		return e
	}
	old, e := s.deps.Rule.Slot(p.Header.Timestamp)
	if e != nil {
		return e
	}
	if slot <= old {
		return core.ErrNoProposal
	}
	h := composed.Header{Timestamp: now}
	m, _, e := s.deps.Rule.Expected(ctx, p, h)
	if e != nil {
		return e
	}
	key, e := s.deps.Signer.PublicKey(ctx)
	if e != nil {
		return e
	}
	if key.Algorithm != m.Key.Algorithm || !bytes.Equal(key.Bytes, m.Key.Bytes) {
		return core.ErrNoProposal
	}
	return nil
}
func (s *Sealer) Seal(ctx context.Context, parent, b core.Block) (core.Block, error) {
	v, e := composed.Unwrap(b)
	if e != nil {
		return nil, e
	}
	slot, e := s.deps.Rule.Slot(v.Header.Timestamp)
	if e != nil {
		return nil, e
	}
	now := s.clock().Unix()
	current, e := s.deps.Rule.Slot(now)
	if e != nil {
		if _, ok := e.(*poa.ScheduleError); ok {
			return nil, core.ErrNoProposal
		}
		return nil, e
	}
	if slot != current || v.Header.Timestamp > now {
		return nil, core.ErrNoProposal
	}
	if len(v.Header.StateRoot) != 32 || len(v.Header.ReceiptsRoot) != 32 {
		return nil, fmt.Errorf("execution-finalized header required")
	}
	old, e := composed.Unwrap(parent)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(v.Header.Parent, []byte(parent.ID())) {
		return nil, core.ErrConflict
	}
	m, consensus, e := s.deps.Rule.Expected(ctx, old, v.Header)
	if e != nil {
		return nil, e
	}
	key, e := s.deps.Signer.PublicKey(ctx)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(v.Header.Producer, []byte(m.ID)) || !bytes.Equal(consensus, v.Header.Consensus) || key.Algorithm != m.Key.Algorithm || !bytes.Equal(key.Bytes, m.Key.Bytes) {
		return nil, core.ErrNoProposal
	}
	domain, payload, e := poa.Signing(v.Header)
	if e != nil {
		return nil, e
	}
	v.Proof, e = s.deps.Signer.Sign(ctx, domain, payload)
	if e != nil {
		return nil, e
	}
	return composed.Wrap(v)
}
