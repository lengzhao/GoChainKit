package account

import (
	"context"
	"errors"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/internal/modeltest"
	"testing"
)

func TestReadAccountSnapshot(t *testing.T) {
	address := assetcap.Address{1}
	state := modeltest.Reader{key(address): encodeAccount(1<<54, 17)}
	got, err := ReadAccount(context.Background(), state, address)
	if err != nil || got.Balance != 1<<54 || got.Nonce != 17 {
		t.Fatal(got, err)
	}
	got, err = ReadAccount(context.Background(), state, assetcap.Address{2})
	if err != nil || got != (Account{}) {
		t.Fatal(got, err)
	}
	state[key(address)] = []byte{1}
	if _, err = ReadAccount(context.Background(), state, address); err == nil {
		t.Fatal("corruption accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ReadAccount(ctx, state, address); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
