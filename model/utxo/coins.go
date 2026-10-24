package utxo

import (
	"context"
	"encoding/binary"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/core"
	"sort"
	"strings"
)

const outputPrefix = "model/utxo/output/"

func (e *Engine) RequiresExecutionContext() bool { return e.maturity != 0 }
func maturityKey(key string) string {
	return "model/utxo/coinbase/" + strings.TrimPrefix(key, outputPrefix)
}

// Coin is an anchored unspent output. Spendable is evaluated at the supplied
// height; callers normally use next candidate height for wallet selection.
type Coin struct {
	Outpoint Outpoint
	Output
	Coinbase      bool
	CreatedHeight uint64
	MatureHeight  uint64
	Spendable     bool
}

// Coins lists an owner's outputs in deterministic outpoint order. It reads one
// immutable snapshot, honors cancellation, and returns caller-owned values.
func (e *Engine) Coins(ctx context.Context, s core.StateReader, owner assetcap.Address, height uint64) ([]Coin, error) {
	state, err := s.Entries(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0)
	for key := range state {
		if strings.HasPrefix(key, outputPrefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := make([]Coin, 0)
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		suffix := strings.TrimPrefix(key, outputPrefix)
		if len(suffix) != 36 {
			return nil, fmt.Errorf("corrupt outpoint")
		}
		value, err := decode(state[key])
		if err != nil {
			return nil, err
		}
		if value.To != owner {
			continue
		}
		coin := Coin{Output: value, Spendable: true}
		copy(coin.Outpoint.TransactionID[:], suffix[:32])
		coin.Outpoint.Index = binary.BigEndian.Uint32([]byte(suffix[32:]))
		if metadata, exists := state[maturityKey(key)]; exists {
			if len(metadata) != 16 {
				return nil, fmt.Errorf("corrupt coinbase metadata")
			}
			coin.Coinbase = true
			coin.CreatedHeight = binary.BigEndian.Uint64(metadata)
			coin.MatureHeight = binary.BigEndian.Uint64(metadata[8:])
			coin.Spendable = height >= coin.MatureHeight
		}
		out = append(out, coin)
	}
	return out, nil
}
