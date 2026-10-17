package integration

import (
	"context"
	"github.com/lengzhao/GoChainKit/core/contracts"
	_ "github.com/lengzhao/GoChainKit/plugins/node/coordinator"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
)

func TestProductionGraph(t *testing.T) {
	graph := map[string]any{
		"store":    config.PluginUse{Use: "storage/memory", Config: map[string]any{"chainID": "dev"}},
		"pool":     config.PluginUse{Use: "mempool/memory", Config: map[string]any{"chainID": "dev", "maxTransactions": 10, "maxBytes": 4096}},
		"executor": config.PluginUse{Use: "executor/kv", Config: map[string]any{"chainID": "dev"}},
		"builder":  config.PluginUse{Use: "builder/basic", Config: map[string]any{"chainID": "dev", "maxTransactionBytes": 4096}, Deps: map[string]any{"pool": "pool", "chain": "store", "states": "store", "executor": "executor"}},
		"host":     config.PluginUse{Use: "node/coordinator", Deps: map[string]any{"builder": "builder", "chain": "store", "states": "store", "executor": "executor", "committer": "store", "pool": "pool"}},
	}
	ctx := context.Background()
	host, result, err := build.Build[contracts.ConsensusHost](ctx, graph, "host")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Instances) != 5 {
		t.Fatalf("shared dependencies duplicated: %d", len(result.Instances))
	}
	block, err := host.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Validate(ctx, block); err != nil {
		t.Fatal(err)
	}
	if err := host.Commit(ctx, block); err != nil {
		t.Fatal(err)
	}
	next, err := host.Propose(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.Header.Height != 2 {
		t.Fatal("graph did not advance head")
	}
}
