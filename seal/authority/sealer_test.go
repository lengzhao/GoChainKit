package authority

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/poa"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"github.com/lengzhao/GoChainKit/validator/static"
	"testing"
	"time"
)

func TestScheduleAndSignedHeader(t *testing.T) {
	ctx := context.Background()
	pub, key, _ := ed25519.GenerateKey(nil)
	members, _ := static.New(static.Config{Members: []valcap.Member{{ID: "a", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: pub}}}})
	rule, _ := poa.New(poa.Config{GenesisTime: 100, SlotSeconds: 2, EpochSlots: 10}, poa.Deps{Members: members, Verifier: &suite.Verifier{}})
	store := keystore.NewMemory()
	store.Create(ctx, "consensus", key)
	signer, _ := custody.New(custody.Config{KeyID: "consensus"}, custody.Deps{Store: store})
	now := int64(100)
	clockReads := 0
	sealer, _ := New(Config{Clock: func() time.Time { clockReads++; return time.Unix(now, 0) }}, Deps{Rule: rule, Signer: signer})
	parent := composed.Envelope{Header: composed.Header{Version: 1, Binding: make([]byte, 32), Timestamp: 100}}
	pb, _ := composed.Wrap(parent)
	if e := sealer.Ready(ctx, pb); !errors.Is(e, core.ErrNoProposal) {
		t.Fatal("same slot signed", e)
	}
	now = 102
	if e := sealer.Ready(ctx, pb); e != nil {
		t.Fatal(e)
	}
	h := composed.Header{Version: 1, Binding: make([]byte, 32), Parent: []byte(parent.ID()), Height: 1, Timestamp: 102, Producer: []byte("a"), StateRoot: make([]byte, 32), ReceiptsRoot: make([]byte, 32)}
	h.Consensus, _ = rule.Draft(ctx, parent, h)
	candidate, _ := composed.Wrap(composed.Envelope{Header: h})
	before := clockReads
	sealed, e := sealer.Seal(ctx, pb, candidate)
	if e != nil {
		t.Fatal(e)
	}
	if clockReads != before+1 {
		t.Fatal("clock sampled multiple times")
	}
	if sealed.ID() != candidate.ID() {
		t.Fatal("certificate altered identity")
	}
	v, _ := composed.Unwrap(sealed)
	if e = rule.Validate(ctx, nil, parent, v, true); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*composed.Envelope){func(v *composed.Envelope) { v.Header.Binding[0]++ }, func(v *composed.Envelope) { v.Header.StateRoot[0]++ }, func(v *composed.Envelope) { v.Proof = bytes.Repeat([]byte{1}, 64) }, func(v *composed.Envelope) { v.Proof = nil }, func(v *composed.Envelope) { v.Header.Producer = []byte("b") }} {
		altered, _ := composed.Unwrap(sealed)
		mutate(&altered)
		if e = rule.Validate(ctx, nil, parent, altered, true); e == nil {
			t.Fatal("tamper accepted")
		}
	}
	now = 100
	if _, e = sealer.Seal(ctx, pb, candidate); !errors.Is(e, core.ErrNoProposal) {
		t.Fatal("signed future slot", e)
	}
	now = 104
	if _, e = sealer.Seal(ctx, pb, candidate); !errors.Is(e, core.ErrNoProposal) {
		t.Fatal("signed stale slot", e)
	}
}

type failingSlotRule struct {
	Rule
	failure error
}

func (r failingSlotRule) Slot(int64) (uint64, error) { return 0, r.failure }
func TestMixedSlotFaultNotRetried(t *testing.T) {
	failure := errors.New("clock policy backend failed")
	mixed := errors.Join(&poa.ScheduleError{Reason: "not yet"}, failure)
	s := &Sealer{deps: Deps{Rule: failingSlotRule{failure: mixed}}, clock: func() time.Time { return time.Unix(100, 0) }}
	parent, _ := composed.Wrap(composed.Envelope{Header: composed.Header{Version: 1, Binding: make([]byte, 32), Timestamp: 100}})
	if e := s.Ready(context.Background(), parent); !errors.Is(e, failure) || errors.Is(e, core.ErrNoProposal) {
		t.Fatal("mixed Ready fault swallowed", e)
	}
	if _, e := s.Seal(context.Background(), parent, parent); !errors.Is(e, failure) || errors.Is(e, core.ErrNoProposal) {
		t.Fatal("mixed Seal fault swallowed", e)
	}
}
