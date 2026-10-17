// Package kv implements the development chain's signed key-value transaction.
package kv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"github.com/lengzhao/pluginkit"
)

type Config struct {
	ChainID string `json:"chainID"`
}
type executor struct{ chainID string }

func init() { pluginkit.Register("executor/kv", New) }
func New(cfg Config) (contracts.Executor, error) {
	if cfg.ChainID == "" || len(cfg.ChainID) > types.MaxChainIDBytes {
		return nil, fmt.Errorf("executor/kv: chainID required")
	}
	return &executor{cfg.ChainID}, nil
}

// ValueKey namespaces application keys by sender and separates them from nonces.
func ValueKey(address types.Address, key []byte) []byte {
	out := make([]byte, 1+len(address)+len(key))
	out[0] = 1
	copy(out[1:], address[:])
	copy(out[33:], key)
	return out
}
func nonceKey(address types.Address) string { return string(append([]byte{0}, address[:]...)) }
func (e *executor) Execute(ctx context.Context, parent contracts.StateReader, b types.Block) (types.ExecutionResult, error) {
	fail := func(err error) (types.ExecutionResult, error) { return types.ExecutionResult{}, err }
	if b.Header.ChainID != e.chainID {
		return fail(fmt.Errorf("wrong block chain"))
	}
	state, err := parent.Entries(ctx)
	if err != nil {
		return fail(err)
	}
	if state == nil {
		state = make(map[string][]byte)
	}
	writes := make(map[string][]byte)
	receipts := make([]types.Receipt, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := tx.Verify(e.chainID); err != nil {
			return fail(fmt.Errorf("%w: %v", contracts.ErrInvalidTransaction, err))
		}
		nk := nonceKey(tx.Sender())
		stored := state[nk]
		var previous uint64
		if len(stored) != 0 {
			if len(stored) != 8 {
				return fail(fmt.Errorf("invalid stored nonce"))
			}
			previous = binary.BigEndian.Uint64(stored)
		}
		if previous == ^uint64(0) || tx.Nonce != previous+1 {
			return fail(fmt.Errorf("%w: nonce is not consecutive", contracts.ErrInvalidTransaction))
		}
		encoded := make([]byte, 8)
		binary.BigEndian.PutUint64(encoded, tx.Nonce)
		key := string(ValueKey(tx.Sender(), tx.Key))
		state[key] = bytes.Clone(tx.Value)
		state[nk] = encoded
		writes[key] = bytes.Clone(tx.Value)
		writes[nk] = bytes.Clone(encoded)
		id, err := tx.ID()
		if err != nil {
			return fail(err)
		}
		receipts = append(receipts, types.Receipt{TransactionID: id, Success: true})
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return types.ExecutionResult{Parent: b.Header.Parent, Writes: writes, Receipts: receipts, StateRoot: types.StateRoot(state)}, nil
}
