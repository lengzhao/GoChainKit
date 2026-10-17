package pow

import (
	"bytes"
	"context"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"math/big"
	"testing"
	"time"
)

func TestHistoricalDifficultyTimeAndRetarget(t *testing.T) {
	ctx := context.Background()
	now := int64(1000000000)
	clock := func() time.Time { return time.Unix(now, 0) }
	r, e := New(Config{RetargetInterval: 4, TargetSpacing: 10, MedianWindow: 3, FutureDrift: 120, Clock: clock})
	if e != nil {
		t.Fatal(e)
	}
	m, _ := account.New(account.Config{Network: "retarget"})
	p, e := composed.NewPoW(composed.Config{Network: "retarget", GenesisTime: now, Clock: clock}, m, r, r)
	if e != nil {
		t.Fatal(e)
	}
	s, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	for height := uint64(1); height <= 4; height++ {
		now = 1000000000 + int64(height)*10
		parentID, _ := s.Head(ctx)
		parent, _ := s.Block(ctx, parentID)
		draft, e := p.Draft(ctx, parent, nil)
		if e != nil {
			t.Fatal(e)
		}
		snap, _ := s.Snapshot(ctx, parentID)
		result, e := p.Execute(ctx, snap, draft)
		snap.Close()
		if e != nil {
			t.Fatal(e)
		}
		b, e := p.Finalize(ctx, draft, result)
		if e != nil {
			t.Fatal(e)
		}
		v, _ := composed.Unwrap(b)
		old, _ := composed.Unwrap(parent)
		proof, _ := DecodeProof(v.Header.Consensus)
		if height == 4 {
			initial, _ := DecodeTarget(r.cfg.InitialBits)
			want := initial.Mul(initial, big.NewInt(30))
			want.Div(want, big.NewInt(40))
			if proof.Bits != EncodeTarget(want) {
				t.Fatal("wrong retarget")
			}
			forged := v
			forged.Header.Consensus = EncodeProof(Proof{Bits: r.cfg.InitialBits, EpochStart: now})
			if e = r.Validate(ctx, s, old, forged, false); e == nil {
				t.Fatal("accepted self-reported easier target")
			}
			invalidTime := v
			invalidTime.Header.Timestamp = now + r.cfg.FutureDrift + 1
			if e = r.Validate(ctx, s, old, invalidTime, false); e == nil {
				t.Fatal("accepted future timestamp")
			}
			staleTime := v
			staleTime.Header.Timestamp = 1000000020
			if e = r.Validate(ctx, s, old, staleTime, false); e == nil {
				t.Fatal("accepted median-time timestamp")
			}
			badEpoch := v
			badProof := proof
			badProof.EpochStart++
			badEpoch.Header.Consensus = EncodeProof(badProof)
			if e = r.Validate(ctx, s, old, badEpoch, false); e == nil {
				t.Fatal("accepted forged epoch")
			}
		}
		if e = r.Validate(ctx, s, old, v, false); e != nil {
			t.Fatal(e)
		}
		b, e = p.Seal(ctx, b)
		if e != nil {
			t.Fatal(e)
		}
		result.BlockID = b.ID()
		if e = s.Commit(ctx, parentID, b, result); e != nil {
			t.Fatal(e)
		}
	}
	parentID, _ := s.Head(ctx)
	parent, _ := s.Block(ctx, parentID)
	envelope, _ := composed.Unwrap(parent)
	w, e := r.Work(envelope)
	if e != nil || w.Sign() <= 0 {
		t.Fatal("invalid work", e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = r.Seal(canceled, envelope); e == nil {
		t.Fatal("ignored mining cancellation")
	}
}
func TestRetargetBoundsRewardsAndBinding(t *testing.T) {
	r, _ := New(Config{RetargetInterval: 4, TargetSpacing: 10, InitialReward: 50, HalvingInterval: 4})
	h := composed.Header{Height: 3, Timestamp: 1000000003, Consensus: EncodeProof(Proof{Bits: r.cfg.InitialBits, EpochStart: 1000000000})}
	parent := composed.Envelope{Header: h}
	next := composed.Header{Height: 4, Timestamp: 1000000004}
	proof, e := r.next(parent, next, 1000000000)
	if e != nil {
		t.Fatal(e)
	}
	base, _ := DecodeTarget(r.cfg.InitialBits)
	want := new(big.Int).Div(base, big.NewInt(4))
	if proof.Bits != EncodeTarget(want) {
		t.Fatal("lower clamp failed")
	}
	parent.Header.Timestamp = 1000001000
	proof, e = r.next(parent, next, 1000000000)
	if e != nil || proof.Bits != r.cfg.LimitBits {
		t.Fatal("upper clamp/limit failed", e)
	}
	if reward, _ := r.Reward(3); reward != 50 {
		t.Fatal(reward)
	}
	if reward, _ := r.Reward(4); reward != 25 {
		t.Fatal(reward)
	}
	if reward, _ := r.Reward(256); reward != 0 {
		t.Fatal(reward)
	}
	changed, _ := New(Config{RetargetInterval: 4, TargetSpacing: 11, InitialReward: 50, HalvingInterval: 4})
	if changed.ID() == r.ID() {
		t.Fatal("semantic configuration not bound")
	}
	data := EncodeProof(Proof{Bits: 0x207fffff, EpochStart: 1000})
	if !bytes.Equal(data, EncodeProof(Proof{Bits: 0x207fffff, EpochStart: 1000})) {
		t.Fatal("nondeterministic proof")
	}
}
