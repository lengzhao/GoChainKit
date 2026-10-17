package integration

import (
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	_ "github.com/lengzhao/GoChainKit/plugins/node/coordinator"
	_ "github.com/lengzhao/GoChainKit/plugins/storage/bolt"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"path/filepath"
	"testing"
)

func productionGraph(store config.PluginUse) map[string]any {
	return map[string]any{
		"store":    store,
		"pool":     config.PluginUse{Use: "mempool/memory", Config: map[string]any{"chainID": "dev", "maxTransactions": 10, "maxBytes": 4096}},
		"executor": config.PluginUse{Use: "executor/kv", Config: map[string]any{"chainID": "dev"}},
		"builder":  config.PluginUse{Use: "builder/basic", Config: map[string]any{"chainID": "dev", "maxTransactionBytes": 4096}, Deps: map[string]any{"pool": "pool", "chain": "store", "states": "store", "executor": "executor"}},
		"host":     config.PluginUse{Use: "node/coordinator", Deps: map[string]any{"builder": "builder", "chain": "store", "states": "store", "executor": "executor", "committer": "store", "pool": "pool"}},
	}
}

func TestProductionGraph(t *testing.T) {
	graph := productionGraph(config.PluginUse{Use: "storage/memory", Config: map[string]any{"chainID": "dev"}})
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

func TestPersistentProductionGraph(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	graph := productionGraph(config.PluginUse{Use: "storage/bolt", Config: map[string]any{"chainID": "dev", "path": path}})
	var confirmed types.Hash
	for phase := 0; phase < 2; phase++ {
		host, result, err := build.Build[contracts.ConsensusHost](ctx, graph, "host")
		if err != nil {
			t.Fatal(err)
		}
		lifecycle, err := build.RequireByID[contracts.Lifecycle](result, "storage", "store")
		if err != nil {
			t.Fatal(err)
		}
		if err := lifecycle.Start(ctx); err != nil {
			t.Fatal(err)
		}
		// Cleanup also runs on an assertion failure; Stop is idempotent.
		t.Cleanup(func() { _ = lifecycle.Stop(context.Background()) })
		pool, err := build.RequireByID[contracts.Mempool](result, "pool", "pool")
		if err != nil {
			t.Fatal(err)
		}
		transactions, err := build.RequireByID[contracts.TransactionStore](result, "transactions", "store")
		if err != nil {
			t.Fatal(err)
		}
		if phase == 1 {
			stored, err := transactions.Transaction(ctx, confirmed)
			if err != nil || stored.Height != 1 {
				t.Fatal("transaction not recovered", err)
			}
		}
		tx := types.Transaction{ChainID: "dev", Nonce: uint64(phase + 1), Key: []byte("persist"), Value: []byte{byte(phase)}}
		if err := tx.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
			t.Fatal(err)
		}
		if err := pool.Add(ctx, tx); err != nil {
			t.Fatal(err)
		}
		block, err := host.Propose(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if block.Header.Height != uint64(phase+1) {
			t.Fatal("recovered height incorrect")
		}
		if err := host.Commit(ctx, block); err != nil {
			t.Fatal(err)
		}
		confirmed, err = tx.ID()
		if err != nil {
			t.Fatal(err)
		}
		if err := lifecycle.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
