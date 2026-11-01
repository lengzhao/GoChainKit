package evm

import (
	"bytes"
	"context"
	"fmt"

	geth "github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lengzhao/GoChainKit/core"
)

type transaction struct {
	raw    []byte
	id     core.ID
	native *types.Transaction
}

func (t *transaction) ID() core.ID { return t.id }

func (e *Engine) DecodeTransaction(raw []byte) (core.Transaction, error) {
	native, err := e.codec.Decode(raw)
	if err != nil {
		return nil, err
	}
	if err = e.validateNative(context.Background(), native); err != nil {
		return nil, err
	}
	return &transaction{raw: bytes.Clone(raw), id: core.ID(string(native.Hash().Bytes())), native: native}, nil
}

func (e *Engine) EncodeTransaction(tx core.Transaction) ([]byte, error) {
	t, ok := tx.(*transaction)
	if !ok || t == nil {
		return nil, fmt.Errorf("evm: foreign transaction")
	}
	return bytes.Clone(t.raw), nil
}

// native borrows the private immutable parsed transaction; it never escapes the
// model. Consumers of mutable bytes receive owned copies through EncodeTransaction.
func (e *Engine) native(tx core.Transaction) (*types.Transaction, error) {
	t, ok := tx.(*transaction)
	if !ok || t == nil || t.native == nil {
		return nil, fmt.Errorf("evm: foreign transaction")
	}
	return t.native, nil
}

func (e *Engine) ValidateTransaction(ctx context.Context, tx core.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	native, err := e.native(tx)
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err)
	}
	return e.validateNative(ctx, native)
}

func (e *Engine) validateNative(ctx context.Context, native *types.Transaction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if native.Type() > types.DynamicFeeTxType || !native.Protected() || native.ChainId().Cmp(e.chain.ChainID) != 0 {
		err = fmt.Errorf("unsupported transaction type or chain")
	} else if native.Gas() > e.cfg.GasLimit || native.Gas() < 21000 || native.GasFeeCap().BitLen() > 256 || native.GasTipCap().BitLen() > 256 || native.GasFeeCap().Cmp(e.baseFee) < 0 || native.GasTipCap().Cmp(native.GasFeeCap()) > 0 {
		err = fmt.Errorf("invalid transaction gas policy")
	} else {
		_, err = geth.TransactionToMessage(native, e.signer, e.baseFee)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalidTransaction, err)
	}
	return ctx.Err()
}
