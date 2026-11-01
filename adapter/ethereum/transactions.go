// Package ethereum adapts go-ethereum transaction encoding and sender recovery.
// Native transaction types, hashing and signing domains remain upstream-owned.
package ethereum

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type Config struct {
	ChainID             string `json:"chainID"`
	MaxTransactionBytes int    `json:"maxTransactionBytes"`
}

// Transactions binds sender recovery to an explicit positive chain ID. It has
// no external resources and is safe for concurrent calls with immutable inputs.
type Transactions struct {
	chainID *big.Int
	signer  types.Signer
	codec   *Codec
}

// Encode delegates native envelope encoding to the chain-independent codec.
func (s *Transactions) Encode(tx *types.Transaction) ([]byte, error) { return s.codec.Encode(tx) }

// Decode parses a canonical native envelope without applying signing policy.
func (s *Transactions) Decode(data []byte) (*types.Transaction, error) { return s.codec.Decode(data) }

func New(c Config) (*Transactions, error) {
	id, ok := new(big.Int).SetString(c.ChainID, 10)
	if !ok || id.Sign() <= 0 || id.BitLen() > 256 {
		return nil, fmt.Errorf("ethereum: positive uint256 decimal chain ID required")
	}
	codec, err := NewCodec(c.MaxTransactionBytes)
	if err != nil {
		return nil, err
	}
	return &Transactions{id, types.LatestSignerForChainID(id), codec}, nil
}

// Sender recovers a signed, replay-protected transaction's address for this chain.
// Latest upstream transaction types are accepted regardless of fork activation.
// Upstream signature recovery is bounded but not interruptible mid-operation.
func (s *Transactions) Sender(ctx context.Context, tx *types.Transaction) (common.Address, error) {
	if err := ctx.Err(); err != nil {
		return common.Address{}, err
	}
	if _, err := s.Encode(tx); err != nil {
		return common.Address{}, err
	}
	if !tx.Protected() || tx.ChainId().Cmp(s.chainID) != 0 {
		return common.Address{}, fmt.Errorf("ethereum: protected transaction for configured chain required")
	}
	sender, err := types.Sender(s.signer, tx)
	if err != nil {
		return common.Address{}, fmt.Errorf("ethereum sender: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return common.Address{}, err
	}
	return sender, nil
}
