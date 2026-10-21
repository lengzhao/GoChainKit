// Package ed25519 implements the ed25519/gck-domain-v1 signing suite.
package ed25519

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	cap "github.com/lengzhao/GoChainKit/cap/signer"
)

// Algorithm fixes raw Ed25519 keys/signatures, no prehash, and the framing below.
const Algorithm = "ed25519/gck-domain-v1"

type Verifier struct{}

func (Verifier) ValidatePublicKey(k cap.PublicKey) error {
	if k.Algorithm != Algorithm || len(k.Bytes) != ed25519.PublicKeySize {
		return fmt.Errorf("ed25519: unsupported suite or malformed public key")
	}
	return nil
}

// frame is the literal prefix followed by four uint64 big-endian length-prefixed
// byte strings: exact chain ID, purpose, version, payload. Empty payload is valid.
func frame(d cap.Domain, p []byte) ([]byte, error) {
	if d.Chain == "" || d.Purpose == "" || d.Version == "" {
		return nil, fmt.Errorf("ed25519: incomplete signing domain")
	}
	b := []byte("GoChainKit/sign/ed25519/v1\x00")
	for _, v := range [][]byte{[]byte(d.Chain), []byte(d.Purpose), []byte(d.Version), p} {
		b = binary.BigEndian.AppendUint64(b, uint64(len(v)))
		b = append(b, v...)
	}
	return b, nil
}

// PublicKey validates the complete raw 64-byte private key, including its public
// half. Returned bytes belong to the caller. Private material is never returned.
func PublicKey(k []byte) (cap.PublicKey, error) {
	if len(k) != ed25519.PrivateKeySize {
		return cap.PublicKey{}, fmt.Errorf("ed25519: malformed private key")
	}
	expected := ed25519.NewKeyFromSeed(k[:ed25519.SeedSize])
	defer clear(expected)
	if !bytes.Equal(expected, k) {
		return cap.PublicKey{}, fmt.Errorf("ed25519: inconsistent private key")
	}
	return cap.PublicKey{Algorithm: Algorithm, Bytes: bytes.Clone(k[ed25519.SeedSize:])}, nil
}
func Sign(k []byte, d cap.Domain, p []byte) ([]byte, error) {
	if _, e := PublicKey(k); e != nil {
		return nil, e
	}
	b, e := frame(d, p)
	if e != nil {
		return nil, e
	}
	return ed25519.Sign(ed25519.PrivateKey(k), b), nil
}
func (v Verifier) Verify(ctx context.Context, k cap.PublicKey, d cap.Domain, p, s []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := v.ValidatePublicKey(k); e != nil {
		return e
	}
	b, e := frame(d, p)
	if e != nil {
		return e
	}
	if len(s) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(k.Bytes), b, s) {
		return fmt.Errorf("ed25519: invalid signature")
	}
	return ctx.Err()
}
