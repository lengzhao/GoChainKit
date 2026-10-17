// Package asset provides address derivation, fee encoding and checked arithmetic
// helpers. Optional currency and escrow contracts are defined in cap/asset.
package asset

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
)

func AddressOf(publicKey []byte) assetcap.Address { return sha256.Sum256(publicKey) }

func Fees(change model.Transition) (uint64, error) {
	var total uint64
	found := false
	for _, effect := range change.Effects {
		if effect.Kind == assetcap.FeeEffect {
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
	return model.Effect{Kind: assetcap.FeeEffect, Data: binary.BigEndian.AppendUint64(nil, value)}
}
func Add(a, b uint64) (uint64, error) {
	if ^uint64(0)-a < b {
		return 0, fmt.Errorf("asset amount overflow")
	}
	return a + b, nil
}
