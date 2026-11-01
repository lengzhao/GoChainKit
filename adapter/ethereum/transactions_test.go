package ethereum_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/lengzhao/GoChainKit/adapter/ethereum"
)

func TestNativeRoundTripAndSender(t *testing.T) {
	s, err := ethereum.New(ethereum.Config{ChainID: "1337"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, inner := range []types.TxData{
		&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(2)},
		&types.DynamicFeeTx{ChainID: big.NewInt(1337), Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Value: big.NewInt(2)},
	} {
		tx, err := types.SignTx(types.NewTx(inner), types.LatestSignerForChainID(big.NewInt(1337)), key)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.Encode(tx)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := s.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		if tx.Hash() != decoded.Hash() {
			t.Fatal("transaction hash changed")
		}
		got, err := s.Sender(context.Background(), decoded)
		if err != nil || got != crypto.PubkeyToAddress(key.PublicKey) {
			t.Fatalf("sender %v: %v", got, err)
		}
		wrong, _ := ethereum.New(ethereum.Config{ChainID: "1"})
		if _, err = wrong.Sender(context.Background(), decoded); err == nil {
			t.Fatal("wrong chain accepted")
		}
		if _, err = s.Decode(append(raw, 0)); err == nil {
			t.Fatal("trailing data accepted")
		}
	}
	unsigned := types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(1)})
	if _, err = s.Sender(context.Background(), unsigned); err == nil {
		t.Fatal("unsigned transaction accepted")
	}
	unprotected, err := types.SignTx(unsigned, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sender(context.Background(), unprotected); err == nil {
		t.Fatal("unprotected transaction accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Sender(ctx, unsigned); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
}

func TestLimitsAndConfiguration(t *testing.T) {
	for _, id := range []string{"", "0", "-1", "abc"} {
		if _, err := ethereum.New(ethereum.Config{ChainID: id}); err == nil {
			t.Fatalf("accepted chain ID %q", id)
		}
	}
	s, _ := ethereum.New(ethereum.Config{ChainID: "1", MaxTransactionBytes: 1})
	if _, err := s.Decode([]byte{1, 2}); err == nil {
		t.Fatal("decode budget ignored")
	}
	if _, err := s.Encode(types.NewTx(&types.LegacyTx{})); err == nil {
		t.Fatal("encode budget ignored")
	}
	if _, err := s.Encode(nil); err == nil {
		t.Fatal("nil transaction accepted")
	}
	if _, err := s.Sender(context.Background(), nil); err == nil {
		t.Fatal("nil transaction accepted")
	}
}
