// Package poa validates deterministic rotating-authority signatures. It provides
// neither Byzantine finality nor an election/penalty mechanism.
package poa

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
)

// ScheduleError denotes a deterministic slot/member mismatch. Dependency faults
// are never wrapped in this type; local proposal production may retry it.
type ScheduleError struct{ Reason string }

func (e *ScheduleError) Error() string { return "PoA schedule: " + e.Reason }
func localSchedule(e error) error {
	if _, ok := e.(*ScheduleError); ok {
		return fmt.Errorf("%s: %w", e, core.ErrNoProposal)
	}
	return e
}

type Config struct {
	GenesisTime int64  `json:"genesisTime"`
	SlotSeconds int64  `json:"slotSeconds"`
	EpochSlots  uint64 `json:"epochSlots"`
}
type Verifier interface {
	signcap.Verifier
	signcap.KeyValidator
}
type Deps struct {
	Members  valcap.Provider `json:"members"`
	Verifier Verifier        `json:"verifier"`
}
type Rules struct {
	cfg  Config
	deps Deps
	id   string
}

func New(c Config, d Deps) (*Rules, error) {
	if c.GenesisTime <= 0 || c.SlotSeconds <= 0 || c.EpochSlots == 0 || d.Members == nil || d.Verifier == nil || d.Members.Identity() == "" {
		return nil, fmt.Errorf("PoA configuration and dependencies required")
	}
	r := &Rules{cfg: c, deps: d}
	data, _ := json.Marshal(struct {
		Config  Config
		Members []byte
	}{c, []byte(d.Members.Identity())})
	r.id = "rotating-poa/v1/" + hex.EncodeToString(composed.DoubleHash(data))
	return r, nil
}
func (r *Rules) ID() string { return r.id }
func (r *Rules) members(ctx context.Context, parent core.Block) (valcap.Set, error) {
	s, e := r.deps.Members.Members(ctx, parent)
	if e != nil {
		return s, e
	}
	if s.Anchor != parent.ID() || s.ID == "" || len(s.Members) == 0 {
		return s, fmt.Errorf("invalid membership anchor/set")
	}
	ids := map[core.ID]bool{}
	keys := map[string]bool{}
	for _, m := range s.Members {
		k := m.Key.Algorithm + "\x00" + string(m.Key.Bytes)
		if m.ID == "" || ids[m.ID] || keys[k] {
			return s, fmt.Errorf("invalid duplicate member")
		}
		if e = r.deps.Verifier.ValidatePublicKey(m.Key); e != nil {
			return s, e
		}
		ids[m.ID] = true
		keys[k] = true
	}
	return s, nil
}
func (r *Rules) Slot(timestamp int64) (uint64, error) {
	if timestamp < r.cfg.GenesisTime {
		return 0, &ScheduleError{Reason: "timestamp before genesis"}
	}
	return uint64((timestamp - r.cfg.GenesisTime) / r.cfg.SlotSeconds), nil
}
func encode(slot, epoch uint64, id core.ID) []byte {
	b := binary.BigEndian.AppendUint64(nil, slot)
	b = binary.BigEndian.AppendUint64(b, epoch)
	return append(b, []byte(id)...)
}
func (r *Rules) Genesis(h composed.Header) ([]byte, error) {
	if h.Timestamp != r.cfg.GenesisTime {
		return nil, fmt.Errorf("PoA genesis time mismatch")
	}
	// Genesis has no validated parent: membership initialization receives the
	// actual provisional genesis envelope, before its consensus commitment.
	genesis, e := composed.Wrap(composed.Envelope{Header: h})
	if e != nil {
		return nil, e
	}
	s, e := r.members(context.Background(), genesis)
	if e != nil {
		return nil, e
	}
	return encode(0, 0, s.ID), nil
}

// Expected checks the header's scheduling inputs without reading a local clock.
func (r *Rules) Expected(ctx context.Context, parent composed.Envelope, h composed.Header) (valcap.Member, []byte, error) {
	if e := ctx.Err(); e != nil {
		return valcap.Member{}, nil, e
	}
	slot, e := r.Slot(h.Timestamp)
	if e != nil {
		return valcap.Member{}, nil, e
	}
	previous, e := r.Slot(parent.Header.Timestamp)
	if e != nil {
		return valcap.Member{}, nil, e
	}
	if slot == 0 || slot <= previous || h.Timestamp <= parent.Header.Timestamp {
		return valcap.Member{}, nil, &ScheduleError{Reason: "slot must advance"}
	}
	pb, e := composed.Wrap(parent)
	if e != nil {
		return valcap.Member{}, nil, e
	}
	return r.Authority(ctx, pb, h.Timestamp)
}
func (r *Rules) Draft(ctx context.Context, parent composed.Envelope, h composed.Header) ([]byte, error) {
	_, b, e := r.Expected(ctx, parent, h)
	return b, localSchedule(e)
}
func (r *Rules) ValidateCandidate(ctx context.Context, _ core.ChainStore, parent, b composed.Envelope) error {
	m, expected, e := r.Expected(ctx, parent, b.Header)
	if e != nil {
		return e
	}
	if !bytes.Equal(expected, b.Header.Consensus) || !bytes.Equal(b.Header.Producer, []byte(m.ID)) {
		return &ScheduleError{Reason: "invalid member/slot/epoch/set"}
	}
	return nil
}

// Signing binds the entire execution-finalized header and exact chain binding.
func Signing(h composed.Header) (signcap.Domain, []byte, error) {
	if len(h.Binding) != 32 {
		return signcap.Domain{}, nil, fmt.Errorf("invalid chain binding")
	}
	b, e := json.Marshal(h)
	return signcap.Domain{Chain: core.ID(string(h.Binding)), Purpose: "composed/authority-block", Version: "1"}, b, e
}
func (r *Rules) Validate(ctx context.Context, h core.ChainStore, parent, b composed.Envelope, sealed bool) error {
	if e := r.ValidateCandidate(ctx, h, parent, b); e != nil {
		if !sealed {
			return localSchedule(e)
		}
		return e
	}
	if !sealed {
		if len(b.Proof) != 0 {
			return fmt.Errorf("proposal already sealed")
		}
		return nil
	}
	m, _, e := r.Expected(ctx, parent, b.Header)
	if e != nil {
		return e
	}
	domain, payload, e := Signing(b.Header)
	if e != nil {
		return e
	}
	return r.deps.Verifier.Verify(ctx, m.Key, domain, payload, b.Proof)
}

var _ composed.Rule = (*Rules)(nil)

// Authority resolves a header's signing member against its exact parent anchor.
func (r *Rules) Authority(ctx context.Context, parent core.Block, timestamp int64) (valcap.Member, []byte, error) {
	slot, e := r.Slot(timestamp)
	if e != nil || slot == 0 {
		return valcap.Member{}, nil, fmt.Errorf("invalid production slot")
	}
	s, e := r.members(ctx, parent)
	if e != nil {
		return valcap.Member{}, nil, e
	}
	return s.Members[(slot-1)%uint64(len(s.Members))], encode(slot, slot/r.cfg.EpochSlots, s.ID), nil
}
