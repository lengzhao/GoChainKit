// Package pow provides configurable Bitcoin-style proof-of-work rules without
// imposing Bitcoin transaction, wire or block serialization formats.
package pow

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"math"
	"math/big"
	"sort"
	"time"
)

type Config struct {
	InitialBits      uint32           `json:"initialBits"`
	LimitBits        uint32           `json:"limitBits"`
	RetargetInterval uint64           `json:"retargetInterval"`
	TargetSpacing    int64            `json:"targetSpacing"`
	MedianWindow     int              `json:"medianWindow"`
	FutureDrift      int64            `json:"futureDrift"`
	InitialReward    uint64           `json:"initialReward"`
	HalvingInterval  uint64           `json:"halvingInterval"`
	Currency         bool             `json:"currency"`
	Clock            func() time.Time `json:"-"`
}
type Rules struct {
	cfg      Config
	limit    *big.Int
	identity string
}
type Proof struct {
	Bits       uint32
	EpochStart int64
	Nonce      uint64
}

func EncodeProof(p Proof) []byte {
	out := binary.BigEndian.AppendUint32(nil, p.Bits)
	out = binary.BigEndian.AppendUint64(out, uint64(p.EpochStart))
	return binary.BigEndian.AppendUint64(out, p.Nonce)
}
func DecodeProof(data []byte) (Proof, error) {
	if len(data) != 20 {
		return Proof{}, fmt.Errorf("invalid PoW header")
	}
	p := Proof{binary.BigEndian.Uint32(data), int64(binary.BigEndian.Uint64(data[4:])), binary.BigEndian.Uint64(data[12:])}
	if p.EpochStart <= 0 {
		return Proof{}, fmt.Errorf("invalid epoch time")
	}
	if _, e := DecodeTarget(p.Bits); e != nil {
		return Proof{}, e
	}
	return p, nil
}
func New(c Config) (*Rules, error) {
	if c.InitialBits == 0 {
		c.InitialBits = 0x207fffff
	}
	if c.LimitBits == 0 {
		c.LimitBits = 0x207fffff
	}
	if c.RetargetInterval == 0 {
		c.RetargetInterval = 2016
	}
	if c.TargetSpacing == 0 {
		c.TargetSpacing = 600
	}
	if c.MedianWindow == 0 {
		c.MedianWindow = 11
	}
	if c.FutureDrift == 0 {
		c.FutureDrift = 7200
	}
	if c.HalvingInterval == 0 {
		c.HalvingInterval = 210000
	}
	initial, e := DecodeTarget(c.InitialBits)
	if e != nil {
		return nil, e
	}
	limit, e := DecodeTarget(c.LimitBits)
	if e != nil {
		return nil, e
	}
	if initial.Cmp(limit) > 0 || c.RetargetInterval < 2 || c.RetargetInterval > 1000000 || c.TargetSpacing <= 0 || uint64(c.TargetSpacing) > uint64(math.MaxInt64)/c.RetargetInterval/4 || c.MedianWindow < 1 || c.MedianWindow > 101 || c.FutureDrift < 1 {
		return nil, fmt.Errorf("invalid PoW parameters")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	data, _ := json.Marshal(c)
	return &Rules{cfg: c, limit: limit, identity: "bitcoin-style-pow/v1/" + hex.EncodeToString(composed.DoubleHash(data))}, nil
}
func (r *Rules) ID() string    { return r.identity }
func (r *Rules) Enabled() bool { return r.cfg.Currency || r.cfg.InitialReward > 0 }
func (r *Rules) Reward(height uint64) (uint64, error) {
	if height == 0 {
		return 0, nil
	}
	halvings := height / r.cfg.HalvingInterval
	if halvings >= 64 {
		return 0, nil
	}
	return r.cfg.InitialReward >> halvings, nil
}
func (r *Rules) Genesis(h composed.Header) ([]byte, error) {
	return EncodeProof(Proof{Bits: r.cfg.InitialBits, EpochStart: h.Timestamp}), nil
}
func (r *Rules) next(old composed.Envelope, next composed.Header, epoch int64) (Proof, error) {
	previous, e := DecodeProof(old.Header.Consensus)
	if e != nil {
		return Proof{}, e
	}
	target, e := DecodeTarget(previous.Bits)
	if e != nil {
		return Proof{}, e
	}
	bits := previous.Bits
	if next.Height%r.cfg.RetargetInterval == 0 {
		expected := int64(r.cfg.RetargetInterval) * r.cfg.TargetSpacing
		actual := old.Header.Timestamp - epoch
		minimum := expected / 4
		if minimum < 1 {
			minimum = 1
		}
		if actual < minimum {
			actual = minimum
		}
		if actual > expected*4 {
			actual = expected * 4
		}
		target.Mul(target, big.NewInt(actual))
		target.Div(target, big.NewInt(expected))
		if target.Sign() <= 0 {
			target.SetInt64(1)
		}
		if target.Cmp(r.limit) > 0 {
			target.Set(r.limit)
		}
		bits = EncodeTarget(target)
		epoch = next.Timestamp
	}
	return Proof{Bits: bits, EpochStart: epoch}, nil
}
func (r *Rules) Draft(ctx context.Context, parent composed.Envelope, h composed.Header) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	old, e := DecodeProof(parent.Header.Consensus)
	if e != nil {
		return nil, e
	}
	p, e := r.next(parent, h, old.EpochStart)
	if e != nil {
		return nil, e
	}
	return EncodeProof(p), nil
}
func (r *Rules) Validate(ctx context.Context, history core.ChainStore, parent, b composed.Envelope, sealed bool) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	proof, e := DecodeProof(b.Header.Consensus)
	if e != nil {
		return e
	}
	if len(b.Proof) != 0 {
		return fmt.Errorf("PoW has no auxiliary certificate")
	}
	if b.Header.Timestamp > r.cfg.Clock().Unix()+r.cfg.FutureDrift {
		if !sealed {
			return core.ErrNoProposal
		}
		return fmt.Errorf("block too far in future")
	}
	epochHeight := parent.Header.Height - parent.Header.Height%r.cfg.RetargetInterval
	cursor := parent
	var times []int64
	var epoch int64
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		if len(times) < r.cfg.MedianWindow {
			times = append(times, cursor.Header.Timestamp)
		}
		if cursor.Header.Height == epochHeight {
			epoch = cursor.Header.Timestamp
		}
		if cursor.Header.Height == 0 || (len(times) >= r.cfg.MedianWindow && epoch != 0) {
			break
		}
		if history == nil {
			return fmt.Errorf("PoW validation requires history")
		}
		ancestor, e := history.Block(ctx, core.ID(string(cursor.Header.Parent)))
		if e != nil {
			return e
		}
		cursor, e = composed.Unwrap(ancestor)
		if e != nil {
			return e
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if b.Header.Timestamp <= times[len(times)/2] {
		return fmt.Errorf("timestamp does not exceed median time past")
	}
	expected, e := r.next(parent, b.Header, epoch)
	if e != nil {
		return e
	}
	if proof.Bits != expected.Bits || proof.EpochStart != expected.EpochStart {
		return fmt.Errorf("unexpected historical difficulty or epoch")
	}
	if sealed {
		_, e = r.Work(b)
		return e
	}
	return nil
}
func (r *Rules) Work(b composed.Envelope) (*big.Int, error) {
	if b.Header.Height == 0 {
		return big.NewInt(0), nil
	}
	proof, e := DecodeProof(b.Header.Consensus)
	if e != nil {
		return nil, e
	}
	target, e := DecodeTarget(proof.Bits)
	if e != nil {
		return nil, e
	}
	if target.Cmp(r.limit) > 0 {
		return nil, fmt.Errorf("target exceeds limit")
	}
	if new(big.Int).SetBytes([]byte(b.ID())).Cmp(target) > 0 {
		return nil, fmt.Errorf("invalid proof of work")
	}
	return WorkFor(target), nil
}
func (r *Rules) Seal(ctx context.Context, b composed.Envelope) (composed.Envelope, error) {
	proof, e := DecodeProof(b.Header.Consensus)
	if e != nil {
		return composed.Envelope{}, e
	}
	target, e := DecodeTarget(proof.Bits)
	if e != nil {
		return composed.Envelope{}, e
	}
	if target.Cmp(r.limit) > 0 {
		return composed.Envelope{}, fmt.Errorf("target exceeds limit")
	}
	for {
		if e := ctx.Err(); e != nil {
			return composed.Envelope{}, e
		}
		b.Header.Consensus = EncodeProof(proof)
		if new(big.Int).SetBytes([]byte(b.ID())).Cmp(target) <= 0 {
			return b, nil
		}
		if proof.Nonce == math.MaxUint64 {
			return composed.Envelope{}, fmt.Errorf("nonce exhausted")
		}
		proof.Nonce++
	}
}
func (r *Rules) Score(ctx context.Context, _ core.ChainStore, b composed.Envelope, parent []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	w, e := r.Work(b)
	if e != nil {
		return nil, e
	}
	return new(big.Int).Add(new(big.Int).SetBytes(parent), w).Bytes(), nil
}
func (r *Rules) Prefer(ctx context.Context, _ core.ChainStore, _, _ composed.Envelope, a, b []byte) (bool, error) {
	return new(big.Int).SetBytes(b).Cmp(new(big.Int).SetBytes(a)) > 0, ctx.Err()
}
func (r *Rules) Finalized(ctx context.Context, _ core.ChainStore, _ composed.Envelope) (core.ID, error) {
	return "", ctx.Err()
}

var _ composed.WorkRule = (*Rules)(nil)
var _ composed.Choice = (*Rules)(nil)
