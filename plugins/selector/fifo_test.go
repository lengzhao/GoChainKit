package selector

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"testing"
)

func TestFIFOSelectionBudgetAndOwnership(t *testing.T) {
	p := testprotocol.New("test")
	s, err := NewFIFO(FIFOConfig{}, Deps{Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	a := testprotocol.Tx{Label: "a", Body: []byte("value")}
	b := testprotocol.Tx{Label: "b"}
	encoded, _ := p.EncodeTransaction(a)
	selected, err := s.Select(context.Background(), []core.Transaction{a, b}, len(encoded))
	if err != nil || len(selected) != 1 || selected[0].ID() != a.ID() {
		t.Fatal("budget/order", err)
	}
	a.Body[0] = 'x'
	if string(selected[0].(testprotocol.Tx).Body) != "value" {
		t.Fatal("alias")
	}
	if _, err := s.Select(context.Background(), nil, -1); err == nil {
		t.Fatal("negative budget")
	}
}
