package account

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/model/asset"
)

func escrowKey(id core.ID) string     { return "model/account/escrow/" + string(id) }
func escrowUsedKey(id core.ID) string { return "model/account/escrow-used/" + string(id) }
func (e *Engine) Lock(ctx context.Context, parent core.StateReader, lock asset.Lock) (model.Transition, error) {
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
	balance, nonce, err := read(state, lock.Owner)
	if err != nil {
		return model.Transition{}, err
	}
	if balance < lock.Amount {
		return model.Transition{}, fmt.Errorf("insufficient escrow funds")
	}
	record := binary.BigEndian.AppendUint64(append([]byte(nil), lock.Owner[:]...), lock.Amount)
	return model.Transition{Writes: map[string][]byte{key(lock.Owner): encodeAccount(balance-lock.Amount, nonce), k: record}}, nil
}
func (e *Engine) Release(ctx context.Context, parent core.StateReader, release asset.Release) (model.Transition, error) {
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
	balance, nonce, err := read(state, release.To)
	if err != nil {
		return model.Transition{}, err
	}
	balance, err = asset.Add(balance, amount)
	if err != nil {
		return model.Transition{}, err
	}
	return model.Transition{Writes: map[string][]byte{key(release.To): encodeAccount(balance, nonce), escrowUsedKey(release.ID): []byte{1}}, Deletes: []string{k}}, nil
}

var _ asset.Escrow = (*Engine)(nil)
