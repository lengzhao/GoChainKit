package utxo

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model/asset"
	"sort"
	"strings"
)

func escrowKey(id core.ID) string     { return "model/utxo/escrow/" + string(id) }
func escrowUsedKey(id core.ID) string { return "model/utxo/escrow-used/" + string(id) }
func systemOutpoint(domain string, id core.ID) Outpoint {
	hash := sha256.Sum256(append([]byte(domain), []byte(id)...))
	return Outpoint{TransactionID: hash}
}
func (e *Engine) Lock(ctx context.Context, parent core.StateReader, lock assetcap.Lock) (model.Transition, error) {
	if err := ctx.Err(); err != nil {
		return model.Transition{}, err
	}
	if lock.ID == "" || lock.Amount == 0 {
		return model.Transition{}, fmt.Errorf("invalid escrow")
	}
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	k := escrowKey(lock.ID)
	if _, exists := state[k]; exists {
		return model.Transition{}, fmt.Errorf("escrow exists")
	}
	if _, used := state[escrowUsedKey(lock.ID)]; used {
		return model.Transition{}, fmt.Errorf("escrow ID consumed")
	}
	var keys []string
	for key := range state {
		if strings.HasPrefix(key, "model/utxo/output/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := model.Transition{Writes: map[string][]byte{}}
	var selected uint64
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return model.Transition{}, err
		}
		coin, err := decode(state[key])
		if err != nil {
			return model.Transition{}, err
		}
		if coin.To != lock.Owner {
			continue
		}
		selected, err = asset.Add(selected, coin.Amount)
		if err != nil {
			return model.Transition{}, err
		}
		out.Deletes = append(out.Deletes, key)
		if selected >= lock.Amount {
			break
		}
	}
	if selected < lock.Amount {
		return model.Transition{}, fmt.Errorf("insufficient escrow funds")
	}
	if selected > lock.Amount {
		changeKey := outputKey(systemOutpoint("gochainkit/utxo/escrow-change/v1\x00", lock.ID))
		if _, exists := state[changeKey]; exists {
			return model.Transition{}, fmt.Errorf("escrow change collision")
		}
		out.Writes[changeKey] = encode(Output{To: lock.Owner, Amount: selected - lock.Amount})
	}
	out.Writes[k] = binary.BigEndian.AppendUint64(append([]byte(nil), lock.Owner[:]...), lock.Amount)
	return out, nil
}
func (e *Engine) Release(ctx context.Context, parent core.StateReader, release assetcap.Release) (model.Transition, error) {
	if err := ctx.Err(); err != nil {
		return model.Transition{}, err
	}
	if release.ID == "" {
		return model.Transition{}, fmt.Errorf("escrow ID required")
	}
	state, err := parent.Entries(ctx)
	if err != nil {
		return model.Transition{}, err
	}
	k := escrowKey(release.ID)
	record, exists := state[k]
	if !exists {
		return model.Transition{}, core.ErrNotFound
	}
	if len(record) != 40 {
		return model.Transition{}, fmt.Errorf("corrupt escrow")
	}
	amount := binary.BigEndian.Uint64(record[32:])
	if amount == 0 {
		return model.Transition{}, fmt.Errorf("corrupt empty escrow")
	}
	releasedKey := outputKey(systemOutpoint("gochainkit/utxo/escrow-release/v1\x00", release.ID))
	if _, exists := state[releasedKey]; exists {
		return model.Transition{}, fmt.Errorf("escrow release collision")
	}
	return model.Transition{Writes: map[string][]byte{releasedKey: encode(Output{To: release.To, Amount: amount}), escrowUsedKey(release.ID): []byte{1}}, Deletes: []string{k}}, nil
}

var _ assetcap.Escrow = (*Engine)(nil)
