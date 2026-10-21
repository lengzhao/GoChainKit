package keystore_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	store "github.com/lengzhao/GoChainKit/cap/keystore"
	cap "github.com/lengzhao/GoChainKit/cap/signer"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"strings"
	"testing"
)

type fake struct {
	k, last []byte
	err     error
	calls   int
}

func (f *fake) Get(context.Context, store.ID) ([]byte, error) {
	f.calls++
	f.last = bytes.Clone(f.k)
	return f.last, f.err
}
func (*fake) Create(context.Context, store.ID, []byte) error { panic("unexpected create") }
func (*fake) Delete(context.Context, store.ID) error         { panic("unexpected delete") }
func TestCustody(t *testing.T) {
	ctx := context.Background()
	f := &fake{k: ed25519.NewKeyFromSeed(make([]byte, 32))}
	s, e := custody.New(custody.Config{KeyID: store.ID(strings.Repeat("x", 200))}, custody.Deps{Store: f})
	if e != nil || f.calls != 0 {
		t.Fatal(e, f.calls)
	}
	pub, e := s.PublicKey(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(f.last, make([]byte, len(f.last))) {
		t.Fatal("private not cleared")
	}
	d := cap.Domain{Chain: "c", Purpose: "p", Version: "1"}
	sig, e := s.Sign(ctx, d, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = (suite.Verifier{}).Verify(ctx, pub, d, nil, sig); e != nil {
		t.Fatal(e)
	}
	pub.Bytes[0] ^= 1
	p2, e := s.PublicKey(ctx)
	if e != nil || bytes.Equal(pub.Bytes, p2.Bytes) {
		t.Fatal("public alias", e)
	}
	fault := errors.New("SECRET")
	f.err = errors.Join(context.Canceled, fault)
	_, e = s.Sign(ctx, d, nil)
	if !errors.Is(e, fault) || !errors.Is(e, context.Canceled) || strings.Contains(e.Error(), "SECRET") {
		t.Fatal(e)
	}
	if !bytes.Equal(f.last, make([]byte, len(f.last))) {
		t.Fatal("failure not cleared")
	}
	f.err = nil
	f.k[63] ^= 1
	if _, e = s.PublicKey(ctx); e == nil {
		t.Fatal("malformed accepted")
	}
	if !bytes.Equal(f.last, make([]byte, len(f.last))) {
		t.Fatal("invalid key not cleared")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	before := f.calls
	if _, e = s.PublicKey(cancelled); !errors.Is(e, context.Canceled) || f.calls != before {
		t.Fatal("cancel", e)
	}
}
