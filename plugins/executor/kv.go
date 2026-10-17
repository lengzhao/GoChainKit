// Package kv implements the development chain's signed key-value transaction.
package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit"
)

type KVConfig struct {
	ChainID string `json:"chainID"`
}
type Deps struct {
	Protocol core.Protocol `json:"protocol"`
}
type executor struct {
	chainID  string
	protocol core.Protocol
}

func (e *executor) Protocol() core.ProtocolDescriptor { return e.protocol.Protocol() }

func init() { pluginkit.Register("executor/kv", NewKV) }
func NewKV(cfg KVConfig, deps Deps) (core.Executor, error) {
	if cfg.ChainID == "" || len(cfg.ChainID) > protocol.MaxChainIDBytes {
		return nil, fmt.Errorf("executor/kv: chainID required")
	}
	if deps.Protocol == nil || deps.Protocol.Protocol() != (core.ProtocolDescriptor{Name: "kv", Version: "1", Network: cfg.ChainID}) {
		return nil, fmt.Errorf("incompatible protocol")
	}
	return &executor{cfg.ChainID, deps.Protocol}, nil
}

// ValueKey namespaces application keys by sender and separates them from nonces.
func ValueKey(address protocol.Address, key []byte) []byte {
	out := make([]byte, 1+len(address)+len(key))
	out[0] = 1
	copy(out[1:], address[:])
	copy(out[33:], key)
	return out
}
func nonceKey(address protocol.Address) string { return string(append([]byte{0}, address[:]...)) }
func (e *executor) Execute(ctx context.Context, parent core.StateReader, candidate core.Block) (core.ExecutionResult, error) {
	fail := func(err error) (core.ExecutionResult, error) { return core.ExecutionResult{}, err }
	b, unwrapErr := protocol.UnwrapBlock(candidate)
	if unwrapErr != nil {
		return fail(unwrapErr)
	}
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
	receipts := make([]core.Receipt, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := tx.Verify(e.chainID); err != nil {
			return fail(fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err))
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
			return fail(fmt.Errorf("%w: nonce is not consecutive", core.ErrInvalidTransaction))
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
		receipts = append(receipts, core.Receipt{TransactionID: core.ID(string(id[:])), Data: []byte{1}})
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	root, err := e.protocol.StateCommitment(ctx, state)
	if err != nil {
		return fail(err)
	}
	return core.ExecutionResult{Parent: core.ID(string(b.Header.Parent[:])), BlockID: candidate.ID(), Writes: writes, Receipts: receipts, StateRoot: root}, nil
}
