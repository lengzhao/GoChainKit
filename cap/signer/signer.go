// Package signer separates key custody from consensus and transaction rules.
package signer

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// PublicKey owns its bytes. Algorithm names a complete versioned signing suite,
// not merely a primitive such as Ed25519. The suite fixes algorithm parameters,
// key/signature encodings, canonical Domain-plus-payload encoding and prehash
// rules (including whether hashing occurs). Independent implementations of the
// same suite must interoperate; implementations cannot choose their own framing.
// Unsupported or mismatched suites must be rejected explicitly at construction
// or verification, never silently mapped to a different suite.
type PublicKey struct {
	Algorithm string
	Bytes     []byte
}

// Domain must bind an exact chain instance (including genesis), purpose and
// format version. Its canonical encoding with the payload is prescribed by
// PublicKey.Algorithm's signing suite; implementations must use that exact
// encoding and prehash rule rather than select their own framing.
type Domain struct {
	Chain   core.ID
	Purpose string
	Version string
}

// Signer returns caller-owned bytes and never exposes private key material.
// Empty domain fields and unsupported signing suites are errors. The returned
// public key identifies the suite used by Sign.
type Signer interface {
	PublicKey(context.Context) (PublicKey, error)
	Sign(context.Context, Domain, []byte) ([]byte, error)
}

// Verifier does not require private key access. Malformed keys, signatures and
// domains return errors; verification cannot silently choose another suite.
type Verifier interface {
	Verify(context.Context, PublicKey, Domain, []byte, []byte) error
}

// KeyValidator optionally preflights a suite and public key without private key
// access. Consensus constructors can require it alongside Verifier.
type KeyValidator interface{ ValidatePublicKey(PublicKey) error }
