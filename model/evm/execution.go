package evm

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	geth "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	model "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
)

func (e *Engine) Execute(context.Context, core.StateReader, []core.Transaction) (model.Transition, error) {
	return model.Transition{}, fmt.Errorf("evm: block execution context required")
}

func (e *Engine) RequiresExecutionContext() bool { return true }

func (e *Engine) blockContext(height, timestamp uint64, getHash func(uint64) common.Hash) vm.BlockContext {
	return vm.BlockContext{CanTransfer: geth.CanTransfer, Transfer: geth.Transfer, GetHash: getHash, Coinbase: e.coinbase, GasLimit: e.cfg.GasLimit, BlockNumber: new(big.Int).SetUint64(height), Time: timestamp, Difficulty: new(big.Int), BaseFee: new(big.Int).Set(e.baseFee)}
}

func watch(ctx context.Context, evm *vm.EVM) func() {
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { evm.Cancel(); close(finished) })
	return func() {
		if !stop() {
			<-finished
		}
	}
}

func disposition(tx core.Transaction, err error) error {
	kind := core.TransactionRejected
	if errors.Is(err, geth.ErrNonceTooHigh) || errors.Is(err, geth.ErrInsufficientFunds) || errors.Is(err, geth.ErrInsufficientFundsForTransfer) || errors.Is(err, geth.ErrGasLimitReached) {
		kind = core.TransactionDeferred
	}
	return &core.TransactionError{TransactionID: tx.ID(), Disposition: kind, Cause: err}
}

func (e *Engine) ExecuteInContext(ctx context.Context, parent core.StateReader, txs []core.Transaction, execution model.ExecutionContext) (model.Transition, error) {
	if err := ctx.Err(); err != nil {
		return model.Transition{}, err
	}
	heights, ok := execution.(model.HeightContext)
	if !ok {
		return model.Transition{}, fmt.Errorf("evm: height context required")
	}
	times, ok := execution.(model.TimestampContext)
	if !ok || times.Timestamp() < 0 {
		return model.Transition{}, fmt.Errorf("evm: valid timestamp context required")
	}
	hashes, ok := execution.(model.BlockHashContext)
	if !ok {
		return model.Transition{}, fmt.Errorf("evm: ancestor hash context required")
	}
	height, timestamp := heights.Height(), uint64(times.Timestamp())
	m, err := e.restore(ctx, parent)
	if err != nil {
		return model.Transition{}, err
	}
	defer m.close()
	initial := m.initial
	previous := binary.BigEndian.Uint64(initial[heightKey])
	if previous == ^uint64(0) || height != previous+1 {
		return model.Transition{}, fmt.Errorf("evm: noncontiguous execution height")
	}
	var historyErr error
	hashCache := make(map[uint64]common.Hash)
	getHash := func(n uint64) common.Hash {
		if n >= height || height-n > 256 {
			return common.Hash{}
		}
		if hash, ok := hashCache[n]; ok {
			return hash
		}
		id, err := hashes.BlockHash(ctx, n)
		if err != nil {
			historyErr = err
			return common.Hash{}
		}
		if len(id) != 32 {
			historyErr = fmt.Errorf("evm: ancestor hash must be 32 bytes")
			return common.Hash{}
		}
		hash := common.BytesToHash([]byte(id))
		hashCache[n] = hash
		return hash
	}
	evm := vm.NewEVM(e.blockContext(height, timestamp, getHash), m.state, e.chain, vm.Config{})
	stop := watch(ctx, evm)
	defer stop()
	pool := geth.NewGasPool(e.cfg.GasLimit)
	tr := model.Transition{}
	for index, tx := range txs {
		if err := ctx.Err(); err != nil {
			return model.Transition{}, err
		}
		native, err := e.native(tx)
		if err != nil {
			return model.Transition{}, fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err)
		}
		if err := e.ValidateTransaction(ctx, tx); err != nil {
			return model.Transition{}, disposition(tx, err)
		}
		msg, err := geth.TransactionToMessage(native, e.signer, e.baseFee)
		if err != nil {
			return model.Transition{}, disposition(tx, err)
		}
		m.state.SetTxContext(native.Hash(), index, uint32(index))
		evm.SetTxContext(geth.NewEVMTxContext(msg))
		receipt, _, err := geth.ApplyTransactionWithEVM(ctx, msg, pool, m.state, new(big.Int).SetUint64(height), common.Hash{}, timestamp, native, evm)
		if ctx.Err() != nil {
			return model.Transition{}, ctx.Err()
		}
		if historyErr != nil {
			return model.Transition{}, historyErr
		}
		if stateErr := m.state.Error(); stateErr != nil {
			return model.Transition{}, stateErr
		}
		if err != nil {
			return model.Transition{}, disposition(tx, err)
		}
		// Geth's JSON decoder requires the logs array even for transactions that
		// emit no logs. Preserve an empty array rather than JSON null.
		if receipt.Logs == nil {
			receipt.Logs = []*types.Log{}
		}
		data, err := json.Marshal(receipt)
		if err != nil {
			return model.Transition{}, err
		}
		tr.Receipts = append(tr.Receipts, core.Receipt{TransactionID: tx.ID(), Data: data})
	}
	next, err := m.commit(ctx, e.chain.Rules(new(big.Int).SetUint64(height), false, timestamp), height)
	if err != nil {
		return model.Transition{}, err
	}
	next[identityKey] = bytes.Clone(e.identity)
	next[heightKey] = binary.BigEndian.AppendUint64(nil, height)
	next[timeKey] = binary.BigEndian.AppendUint64(nil, timestamp)
	// Retain a bounded ancestor window so calls anchored at this snapshot can use BLOCKHASH.
	for k, v := range initial {
		if strings.HasPrefix(k, hashPrefix) && len(k) == len(hashPrefix)+8 {
			n := binary.BigEndian.Uint64([]byte(k[len(hashPrefix):]))
			if n < height && height-n <= 256 {
				next[k] = bytes.Clone(v)
			}
		}
	}
	parentHash := getHash(height - 1)
	if historyErr != nil {
		return model.Transition{}, historyErr
	}
	next[hashPrefix+string(binary.BigEndian.AppendUint64(nil, height-1))] = bytes.Clone(parentHash[:])
	changes := m.changes(next)
	tr.Writes, tr.Deletes = changes.Writes, changes.Deletes
	return tr, ctx.Err()
}

var _ model.ContextualEngine = (*Engine)(nil)
