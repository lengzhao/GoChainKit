package evm

import (
	"context"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestNativeSnapshotBudgetRoundTrip(t *testing.T) {
	m, err := newStateSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	m.state.SetBalance(common.HexToAddress("0x1000000000000000000000000000000000000001"), uint256.NewInt(7), tracing.BalanceIncreaseGenesisBalance)
	entries, err := m.commit(context.Background(), params.Rules{}, 0)
	m.close()
	if err != nil {
		t.Fatal(err)
	}
	budget := 0
	for k, v := range entries {
		if strings.HasPrefix(k, dbPrefix) {
			budget += len(k) - len(dbPrefix) + len(v)
		}
	}
	if budget == 0 {
		t.Fatal("no native state data")
	}
	restored, err := newStateSessionWithLimit(entries, budget)
	if err != nil {
		t.Fatalf("snapshot exported at its native byte budget cannot be restored: %v", err)
	}
	roundTrip, err := restored.commitWithLimit(context.Background(), params.Rules{}, 0, budget)
	restored.close()
	if err != nil || string(roundTrip[rootKey]) != string(entries[rootKey]) {
		t.Fatalf("boundary export: %v", err)
	}
	if rejected, err := newStateSessionWithLimit(entries, budget-1); err == nil {
		rejected.close()
		t.Fatal("oversized native snapshot restored")
	}
	restored, err = newStateSession(entries)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	if _, err = restored.commitWithLimit(context.Background(), params.Rules{}, 0, budget-1); err == nil {
		t.Fatal("oversized native snapshot exported")
	}
}
