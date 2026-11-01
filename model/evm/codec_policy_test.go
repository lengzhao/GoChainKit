package evm_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
	"github.com/lengzhao/GoChainKit/adapter/ethereum"
	"github.com/lengzhao/GoChainKit/model/evm"
)

func TestSharedCodecDoesNotBroadenLondonPolicy(t *testing.T) {
	key, err := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := types.SignTx(types.NewTx(&types.SetCodeTx{ChainID: uint256.NewInt(1337), To: common.Address{2}, Gas: 100000, GasFeeCap: uint256.NewInt(2), GasTipCap: uint256.NewInt(1), Value: uint256.NewInt(0)}), types.LatestSignerForChainID(big.NewInt(1337)), key)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := ethereum.New(ethereum.Config{ChainID: "1337"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tools.Encode(tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tools.Sender(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	model, err := evm.New(evm.Config{Network: "policy", ChainID: "1337"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = model.DecodeTransaction(raw); err == nil {
		t.Fatal("shared codec silently enabled post-London transaction")
	}
}

func TestFrozenTransactionRespectsConsumingExecutionPolicy(t *testing.T) {
	source, _, _ := setup(t)
	to := common.Address{2}
	tx := signed(t, source, 0, &to, nil, 50000, 1)
	for _, config := range []evm.Config{
		{Network: "gas", ChainID: "1337", GasLimit: 21000},
		{Network: "chain", ChainID: "1"},
		{Network: "fee", ChainID: "1337", BaseFee: "3"},
	} {
		consumer, err := evm.New(config)
		if err != nil {
			t.Fatal(err)
		}
		if err = consumer.ValidateTransaction(context.Background(), tx); err == nil {
			t.Fatalf("cached transaction bypassed consumer policy: %+v", config)
		}
	}
}
