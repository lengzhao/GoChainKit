package bitcoin_test

import (
	"context"
	"errors"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/lengzhao/GoChainKit/core"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
	"testing"
)

func TestFrozenTransactionRespectsConsumingEngineLimit(t *testing.T) {
	large, _, key, script := setup(t)
	tx := spend(t, large, large.GenesisOutpoints()[0], 90000, key, script, 0, wire.MaxTxInSequenceNum)
	small, err := btc.New(btc.Config{Network: "small", GenesisTime: 1700000000, MaxTransactionBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = small.ValidateTransaction(context.Background(), tx); !errors.Is(err, core.ErrInvalidTransaction) {
		t.Fatalf("cached transaction bypassed consuming engine limit: %v", err)
	}
	if _, err = small.Native(tx); err == nil {
		t.Fatal("native accessor bypassed consuming engine limit")
	}
}
