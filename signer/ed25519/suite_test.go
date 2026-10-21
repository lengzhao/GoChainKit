package ed25519_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	cap "github.com/lengzhao/GoChainKit/cap/signer"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	"testing"
)

func TestSuiteIndependentFrame(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	domain := cap.Domain{Chain: "chain", Purpose: "block", Version: "1"}
	payload := []byte("payload")
	frame := []byte("GoChainKit/sign/ed25519/v1\x00")
	for _, v := range [][]byte{[]byte(domain.Chain), []byte(domain.Purpose), []byte(domain.Version), payload} {
		frame = binary.BigEndian.AppendUint64(frame, uint64(len(v)))
		frame = append(frame, v...)
	}
	sig, err := suite.Sign(key, domain, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sig, ed25519.Sign(key, frame)) {
		t.Fatal("framing mismatch")
	}
	v := suite.Verifier{}
	pub := cap.PublicKey{Algorithm: suite.Algorithm, Bytes: key.Public().(ed25519.PublicKey)}
	if err = v.Verify(context.Background(), pub, domain, payload, sig); err != nil {
		t.Fatal(err)
	}
	for _, d := range []cap.Domain{{Chain: "other", Purpose: "block", Version: "1"}, {Chain: "chain", Purpose: "tx", Version: "1"}, {Chain: "chain", Purpose: "block", Version: "2"}, {}} {
		if v.Verify(context.Background(), pub, d, payload, sig) == nil {
			t.Fatal("accepted wrong domain")
		}
	}
	pub.Algorithm = "ed25519"
	if v.Verify(context.Background(), pub, domain, payload, sig) == nil {
		t.Fatal("accepted suite")
	}
	key[63] ^= 1
	if _, err = suite.Sign(key, domain, payload); err == nil {
		t.Fatal("accepted mismatched private key")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if v.Verify(ctx, pub, domain, payload, sig) == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestRejectMalformedAndAmbiguousMessages(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	pub := cap.PublicKey{Algorithm: suite.Algorithm, Bytes: key[32:]}
	d := cap.Domain{Chain: "ab", Purpose: "c", Version: "1"}
	sig, err := suite.Sign(key, d, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	verifier := suite.Verifier{}
	for _, s := range [][]byte{nil, sig[:63], append(append([]byte{}, sig...), 0)} {
		if verifier.Verify(context.Background(), pub, d, []byte("x"), s) == nil {
			t.Fatal("malformed signature accepted")
		}
	}
	if verifier.Verify(context.Background(), pub, d, []byte("y"), sig) == nil {
		t.Fatal("payload substitution")
	}
	if verifier.Verify(context.Background(), pub, cap.Domain{Chain: "a", Purpose: "bc", Version: "1"}, []byte("x"), sig) == nil {
		t.Fatal("ambiguous domain frame")
	}
	wrong := cap.PublicKey{Algorithm: suite.Algorithm, Bytes: make([]byte, 32)}
	if verifier.Verify(context.Background(), wrong, d, []byte("x"), sig) == nil {
		t.Fatal("wrong key")
	}
	wrong.Bytes = nil
	if verifier.ValidatePublicKey(wrong) == nil {
		t.Fatal("malformed key")
	}
	for _, key := range [][]byte{nil, make([]byte, 32), make([]byte, 65)} {
		if _, err = suite.Sign(key, d, nil); err == nil {
			t.Fatal("invalid private key")
		}
	}
}
