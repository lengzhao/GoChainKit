// Package ethereum adapts native Ethereum transaction primitives.
package ethereum

import (
	"bytes"
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
)

// Codec validates bounded canonical transaction envelopes independently of the
// signing suite, fork activation, chain identity or execution policy.
type Codec struct{ maxBytes int }

// NewCodec constructs a codec with a 1 MiB default and a 4 MiB hard ceiling.
func NewCodec(maxBytes int) (*Codec, error) {
	if maxBytes == 0 {
		maxBytes = 1 << 20
	}
	if maxBytes < 1 || maxBytes > 4<<20 {
		return nil, fmt.Errorf("ethereum: transaction limit must be 1..4194304")
	}
	return &Codec{maxBytes: maxBytes}, nil
}

// Encode returns caller-owned native envelope bytes. It does not validate state,
// intrinsic gas, fees or fork activation; those belong to an execution adapter.
func (s *Codec) Encode(tx *types.Transaction) ([]byte, error) {
	if tx == nil {
		return nil, fmt.Errorf("ethereum: transaction required")
	}
	if tx.Size() > uint64(s.maxBytes) {
		return nil, fmt.Errorf("ethereum: transaction exceeds limit")
	}
	data, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("ethereum encode: %w", err)
	}
	if len(data) > s.maxBytes {
		return nil, fmt.Errorf("ethereum: transaction exceeds limit")
	}
	return data, nil
}

// Decode rejects malformed, noncanonical or trailing data, returning a native
// transaction that owns its decoded storage.
func (s *Codec) Decode(data []byte) (*types.Transaction, error) {
	if len(data) == 0 || len(data) > s.maxBytes {
		return nil, fmt.Errorf("ethereum: invalid transaction size")
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(data); err != nil {
		return nil, fmt.Errorf("ethereum decode: %w", err)
	}
	encoded, err := s.Encode(tx)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, encoded) {
		return nil, fmt.Errorf("ethereum: noncanonical transaction")
	}
	return tx, nil
}
