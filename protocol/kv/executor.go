package kv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
)

type executor struct {
	chainID  string
	protocol core.ExecutionProtocol
}

func (e *executor) Protocol() core.ProtocolDescriptor { return e.protocol.Protocol() }

// NewExecutor constructs KV execution semantics independently of plugin assembly.
func NewExecutor(p core.ExecutionProtocol) (core.Executor, error) {
	if p == nil || p.Protocol().Name != "kv" || p.Protocol().Version != "1" {
		return nil, fmt.Errorf("incompatible KV protocol")
	}
	if err := p.Protocol().Validate(); err != nil {
		return nil, err
	}
	return &executor{p.Protocol().Network, p}, nil
}

// ValueKey namespaces application keys by sender and separates them from nonces.
func ValueKey(address Address, key []byte) []byte {
	out := make([]byte, 1+len(address)+len(key))
	out[0] = 1
	copy(out[1:], address[:])
	copy(out[33:], key)
	return out
}
func nonceKey(address Address) string { return string(append([]byte{0}, address[:]...)) }
func (e *executor) Execute(ctx context.Context, parent core.StateReader, candidate core.Block) (core.ExecutionResult, error) {
	fail := func(err error) (core.ExecutionResult, error) { return core.ExecutionResult{}, err }
	b, unwrapErr := UnwrapBlock(candidate)
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
		id, idErr := tx.ID()
		if idErr != nil {
			return fail(idErr)
		}
		if previous == ^uint64(0) || tx.Nonce <= previous {
			return fail(&core.TransactionError{TransactionID: core.ID(string(id[:])), Disposition: core.TransactionRejected, Cause: fmt.Errorf("nonce already consumed")})
		}
		if tx.Nonce != previous+1 {
			return fail(&core.TransactionError{TransactionID: core.ID(string(id[:])), Disposition: core.TransactionDeferred, Cause: fmt.Errorf("nonce predecessor not yet available")})
		}
		encoded := make([]byte, 8)
		binary.BigEndian.PutUint64(encoded, tx.Nonce)
		key := string(ValueKey(tx.Sender(), tx.Key))
		state[key] = bytes.Clone(tx.Value)
		state[nk] = encoded
		writes[key] = bytes.Clone(tx.Value)
		writes[nk] = bytes.Clone(encoded)
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
