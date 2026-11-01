package ethereum_test

import (
	"bytes"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lengzhao/GoChainKit/adapter/ethereum"
	"math/big"
	"testing"
)

func TestCanonicalCodecIsIndependentOfSigningPolicy(t *testing.T) {
	codec, err := ethereum.NewCodec(1024)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(2)})
	raw, err := codec.Encode(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := codec.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codec.Encode(tx)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("unsigned canonical transaction changed")
	}
	if _, err = codec.Decode(append(raw, 0)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	raw[0] ^= 1
	owned, err := codec.Encode(tx)
	if err != nil || !bytes.Equal(owned, again) {
		t.Fatal("decoded transaction aliases input bytes")
	}
	small, _ := ethereum.NewCodec(1)
	if _, err = small.Decode(again); err == nil {
		t.Fatal("decode limit ignored")
	}
	if _, err = small.Encode(tx); err == nil {
		t.Fatal("encode limit ignored")
	}
	if _, err = ethereum.NewCodec(-1); err == nil {
		t.Fatal("negative limit accepted")
	}
}
