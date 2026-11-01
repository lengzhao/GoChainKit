package evm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	geth "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
	model "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
)

// ErrCallInput classifies malformed or over-budget caller-supplied call fields.
// State, history and trusted block-context failures do not use this sentinel.
var ErrCallInput = errors.New("invalid EVM call input")

// ErrCallExecution classifies an EVM execution failure such as REVERT, invalid
// opcode, invalid jump or stack exhaustion. The native VM error stays wrapped.
var ErrCallExecution = errors.New("EVM call execution failed")

// Account reads balance, nonce and code from a caller-owned immutable snapshot.
// It does not enumerate storage; Storage reads a specific slot instead.
func (e *Engine) Account(ctx context.Context, parent core.StateReader, address common.Address) (Account, error) {
	m, err := e.restore(ctx, parent)
	if err != nil {
		return Account{}, err
	}
	defer m.close()
	out := Account{Balance: m.state.GetBalance(address).ToBig().String(), Nonce: m.state.GetNonce(address), Code: bytes.Clone(m.state.GetCode(address))}
	if err = m.state.Error(); err != nil {
		return Account{}, err
	}
	return out, ctx.Err()
}

func (e *Engine) Storage(ctx context.Context, parent core.StateReader, address common.Address, key common.Hash) (common.Hash, error) {
	m, err := e.restore(ctx, parent)
	if err != nil {
		return common.Hash{}, err
	}
	defer m.close()
	value := m.state.GetState(address, key)
	if err = m.state.Error(); err != nil {
		return common.Hash{}, err
	}
	return value, ctx.Err()
}

type CallRequest struct {
	From  common.Address `json:"from"`
	To    common.Address `json:"to"`
	Data  []byte         `json:"data"`
	Value string         `json:"value,omitempty"`
	Gas   uint64         `json:"gas"`
}

type CallResult struct {
	Data     []byte `json:"data"`
	GasUsed  uint64 `json:"gasUsed"`
	Reverted bool   `json:"reverted"`
}

// Call executes a message against an isolated copy without committing any writes.
// It uses the snapshot's block context, with zero gas price and no fee charging.
// Genesis has no protocol timestamp in model state; use CallInContext for queries
// requiring the exact genesis environment from the surrounding chain protocol.
// EVM failures return their native error alongside returndata and gas usage.
func (e *Engine) Call(ctx context.Context, parent core.StateReader, request CallRequest) (CallResult, error) {
	return e.call(ctx, parent, request, nil)
}

// CallInContext uses an explicitly anchored, trusted chain context. Its height
// must match the supplied snapshot; the caller obtains both from the same block.
// External callers must not be allowed to choose this context's environment.
func (e *Engine) CallInContext(ctx context.Context, parent core.StateReader, request CallRequest, execution model.ExecutionContext) (CallResult, error) {
	if execution == nil {
		return CallResult{}, fmt.Errorf("evm: anchored call context required")
	}
	return e.call(ctx, parent, request, execution)
}

func (e *Engine) call(ctx context.Context, parent core.StateReader, request CallRequest, execution model.ExecutionContext) (CallResult, error) {
	if err := ctx.Err(); err != nil {
		return CallResult{}, err
	}
	if request.Gas == 0 {
		request.Gas = e.cfg.GasLimit
	}
	if request.Gas > e.cfg.GasLimit || len(request.Data) > maxTransactionBytes {
		return CallResult{}, fmt.Errorf("%w: call exceeds budget", ErrCallInput)
	}
	if request.Value == "" {
		request.Value = "0"
	}
	value, err := decimal(request.Value)
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: %v", ErrCallInput, err)
	}
	amount, _ := uint256.FromBig(value)
	m, err := e.restore(ctx, parent)
	if err != nil {
		return CallResult{}, err
	}
	defer m.close()
	entries := m.initial
	height, timestamp := binary.BigEndian.Uint64(entries[heightKey]), binary.BigEndian.Uint64(entries[timeKey])
	var anchoredHashes model.BlockHashContext
	if execution != nil {
		heights, ok := execution.(model.HeightContext)
		if !ok || heights.Height() != height {
			return CallResult{}, fmt.Errorf("evm: call context and snapshot height mismatch")
		}
		times, ok := execution.(model.TimestampContext)
		if !ok || times.Timestamp() < 0 {
			return CallResult{}, fmt.Errorf("evm: call timestamp context required")
		}
		anchoredHashes, ok = execution.(model.BlockHashContext)
		if !ok {
			return CallResult{}, fmt.Errorf("evm: call ancestor hash context required")
		}
		if height != 0 && uint64(times.Timestamp()) != timestamp {
			return CallResult{}, fmt.Errorf("evm: call context and snapshot timestamp mismatch")
		}
		timestamp = uint64(times.Timestamp())
	}
	var historyErr error
	cache := make(map[uint64]common.Hash)
	getHash := func(n uint64) common.Hash {
		if n >= height || height-n > 256 {
			return common.Hash{}
		}
		if hash, ok := cache[n]; ok {
			return hash
		}
		var hash common.Hash
		if anchoredHashes == nil {
			hash = common.BytesToHash(entries[hashPrefix+string(binary.BigEndian.AppendUint64(nil, n))])
		} else {
			id, err := anchoredHashes.BlockHash(ctx, n)
			if err != nil {
				historyErr = err
				return common.Hash{}
			}
			if len(id) != 32 {
				historyErr = fmt.Errorf("evm: call ancestor hash must be 32 bytes")
				return common.Hash{}
			}
			hash = common.BytesToHash([]byte(id))
		}
		cache[n] = hash
		return hash
	}
	evm := vm.NewEVM(e.blockContext(height, timestamp, getHash), m.state, e.chain, vm.Config{NoBaseFee: true})
	stop := watch(ctx, evm)
	defer stop()
	msg := &geth.Message{From: request.From, To: &request.To, Value: amount, GasLimit: request.Gas, GasPrice: new(uint256.Int), GasFeeCap: new(uint256.Int), GasTipCap: new(uint256.Int), Data: bytes.Clone(request.Data)}
	msg.SkipNonceCheck().SkipEOACheck()
	evm.SetTxContext(geth.NewEVMTxContext(msg))
	result, err := geth.ApplyMessage(evm, msg, geth.NewGasPool(request.Gas))
	if ctx.Err() != nil {
		return CallResult{}, ctx.Err()
	}
	if historyErr != nil {
		return CallResult{}, historyErr
	}
	if stateErr := m.state.Error(); stateErr != nil {
		return CallResult{}, stateErr
	}
	if err != nil {
		return CallResult{}, fmt.Errorf("%w: %w", ErrCallInput, err)
	}
	output := CallResult{bytes.Clone(result.ReturnData), result.UsedGas, errors.Is(result.Err, vm.ErrExecutionReverted)}
	if result.Err != nil {
		return output, fmt.Errorf("%w: %w", ErrCallExecution, result.Err)
	}
	return output, nil
}
