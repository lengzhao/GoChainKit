package poa

import (
	"bytes"
	"context"
	"crypto/ed25519"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	"github.com/lengzhao/GoChainKit/validator/static"
	"testing"
)

func TestDeterministicSlotsAndEmptyProof(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	members, _ := static.New(static.Config{Members: []valcap.Member{{ID: "a", Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: pub}}}})
	r, e := New(Config{GenesisTime: 100, SlotSeconds: 2, EpochSlots: 4}, Deps{Members: members, Verifier: &suite.Verifier{}})
	if e != nil {
		t.Fatal(e)
	}
	old := composed.Envelope{Header: composed.Header{Version: 1, Binding: make([]byte, 32), Timestamp: 100}}
	old.Header.Consensus, e = r.Genesis(old.Header)
	if e != nil {
		t.Fatal(e)
	}
	h := composed.Header{Version: 1, Binding: make([]byte, 32), Height: 1, Timestamp: 102, Producer: []byte("a"), Parent: []byte(old.ID())}
	h.Consensus, e = r.Draft(context.Background(), old, h)
	if e != nil {
		t.Fatal(e)
	}
	again, _ := r.Draft(context.Background(), old, h)
	if !bytes.Equal(again, h.Consensus) {
		t.Fatal("non deterministic")
	}
	b := composed.Envelope{Header: h}
	if e = r.Validate(context.Background(), nil, old, b, false); e != nil {
		t.Fatal(e)
	}
	if e = r.Validate(context.Background(), nil, old, b, true); e == nil {
		t.Fatal("unsigned empty block accepted")
	}
	b.Header.Producer = []byte("wrong")
	if e = r.ValidateCandidate(context.Background(), nil, old, b); e == nil {
		t.Fatal("wrong producer")
	}
	_ = core.ID("")
}

func TestRotationBindingAndUnknownSuite(t *testing.T) {
	ctx := context.Background()
	members := make([]valcap.Member, 2)
	for i, id := range []core.ID{"a", "b"} {
		pub, _, _ := ed25519.GenerateKey(nil)
		members[i] = valcap.Member{ID: id, Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: pub}}
	}
	provider, _ := static.New(static.Config{Members: members})
	c := Config{GenesisTime: 100, SlotSeconds: 2, EpochSlots: 2}
	r, _ := New(c, Deps{Members: provider, Verifier: &suite.Verifier{}})
	parent := composed.Envelope{Header: composed.Header{Version: 1, Binding: make([]byte, 32), Timestamp: 100}}
	for _, tc := range []struct {
		timestamp int64
		producer  string
		epoch     uint64
	}{{102, "a", 0}, {104, "b", 1}, {106, "a", 1}} {
		h := composed.Header{Timestamp: tc.timestamp, Producer: []byte(tc.producer)}
		data, e := r.Draft(ctx, parent, h)
		if e != nil {
			t.Fatal(e)
		}
		h.Consensus = data
		if e = r.ValidateCandidate(ctx, nil, parent, composed.Envelope{Header: h}); e != nil {
			t.Fatal(e)
		}
		h.Consensus[15]++
		if e = r.ValidateCandidate(ctx, nil, parent, composed.Envelope{Header: h}); e == nil {
			t.Fatal("forged epoch")
		}
	}
	for _, cc := range []Config{{100, 3, 2}, {100, 2, 3}, {101, 2, 2}} {
		changed, _ := New(cc, Deps{Members: provider, Verifier: &suite.Verifier{}})
		if changed.ID() == r.ID() {
			t.Fatal("config unbound")
		}
	}
	members[0].Key.Algorithm = "unknown"
	bad, _ := static.New(static.Config{Members: members})
	r, _ = New(c, Deps{Members: bad, Verifier: &suite.Verifier{}})
	if _, e := r.Genesis(parent.Header); e == nil {
		t.Fatal("unsupported suite accepted during protocol construction")
	}
}
