package memory

import (
	"context"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
)

func TestPluginKitAssembly(t *testing.T) {
	graph := map[string]any{"pool": config.PluginUse{Use: "mempool/memory", Config: map[string]any{"chainID": "dev", "maxTransactions": 1, "maxBytes": 4096}}}
	p, _, err := build.Build[contracts.Mempool](context.Background(), graph, "pool")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(context.Background(), transaction(t, 1)); err != nil {
		t.Fatal(err)
	}
	second, _, err := build.Build[contracts.Mempool](context.Background(), graph, "pool")
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.Select(context.Background(), 4096)
	if err != nil || len(got) != 0 {
		t.Fatal("instances share state", err)
	}
}
