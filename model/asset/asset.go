// Package asset defines optional currency capabilities for asset-bearing models.
// Consensus-free models implement model.Engine without importing this package.
package asset

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
)

type Address [32]byte

func AddressOf(publicKey []byte) Address { return sha256.Sum256(publicKey) }

type Allocation struct {
	Address Address
	Amount  uint64
}
type Mint struct {
	ID     core.ID
	To     Address
	Amount uint64
}
type Currency interface {
	model.Engine
	Credit(context.Context, core.StateReader, Mint) (model.Transition, error)
}

const FeeEffect = "asset/fees/v1"

func Fees(change model.Transition) (uint64, error) {
	var total uint64
	found := false
	for _, effect := range change.Effects {
		if effect.Kind == FeeEffect {
			if found || len(effect.Data) != 8 {
				return 0, fmt.Errorf("invalid fee effect")
			}
			total = binary.BigEndian.Uint64(effect.Data)
			found = true
		}
	}
	return total, nil
}
func Fee(value uint64) model.Effect {
	return model.Effect{Kind: FeeEffect, Data: binary.BigEndian.AppendUint64(nil, value)}
}
func Add(a, b uint64) (uint64, error) {
	if ^uint64(0)-a < b {
		return 0, fmt.Errorf("asset amount overflow")
	}
	return a + b, nil
}
