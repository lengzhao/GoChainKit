package kv_test

import (
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"testing"
)

func TestDomainExecutorConstruction(t *testing.T) {
	p, err := kv.New(kv.Config{ChainID: "domain"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := kv.NewExecutor(p)
	if err != nil || e.Protocol() != p.Protocol() {
		t.Fatalf("domain executor: %v", err)
	}
	if _, err := kv.NewExecutor(nil); err == nil {
		t.Fatal("missing protocol accepted")
	}
}
