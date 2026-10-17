package integration

import (
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	_ "github.com/lengzhao/GoChainKit/plugins/builder/basic"
	_ "github.com/lengzhao/GoChainKit/plugins/executor/kv"
	_ "github.com/lengzhao/GoChainKit/plugins/mempool/memory"
	_ "github.com/lengzhao/GoChainKit/plugins/node/coordinator"
	_ "github.com/lengzhao/GoChainKit/plugins/protocol/kv"
	_ "github.com/lengzhao/GoChainKit/plugins/storage/bolt"
	_ "github.com/lengzhao/GoChainKit/plugins/storage/memory"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/GoChainKit/protocol/kv/types"
	"github.com/lengzhao/pluginkit"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"path/filepath"
	"testing"
)

func init() {
	pluginkit.Register("protocol/integration-flex", func() (contracts.Protocol, error) { return testprotocol.New("flex"), nil })
	pluginkit.Register("executor/integration-flex", func(_ struct{}, d struct {
		Protocol contracts.Protocol `json:"protocol"`
	}) (contracts.Executor, error) {
		return testprotocol.Executor{P: d.Protocol}, nil
	})
}
func productionGraph(store config.PluginUse, custom bool) map[string]any {
	protocol := config.PluginUse{Use: "protocol/kv", Config: map[string]any{"chainID": "dev"}}
	executor := config.PluginUse{Use: "executor/kv", Config: map[string]any{"chainID": "dev"}, Deps: map[string]any{"protocol": "protocol"}}
	if custom {
		protocol = config.PluginUse{Use: "protocol/integration-flex"}
		executor = config.PluginUse{Use: "executor/integration-flex", Deps: map[string]any{"protocol": "protocol"}}
	}
	store.Deps = map[string]any{"protocol": "protocol"}
	return map[string]any{"protocol": protocol, "store": store, "pool": config.PluginUse{Use: "mempool/memory", Config: map[string]any{"maxTransactions": 10, "maxBytes": 16384}, Deps: map[string]any{"protocol": "protocol"}}, "executor": executor, "builder": config.PluginUse{Use: "builder/basic", Config: map[string]any{"maxTransactionBytes": 16384}, Deps: map[string]any{"protocol": "protocol", "pool": "pool", "chain": "store", "states": "store", "executor": "executor"}}, "host": config.PluginUse{Use: "node/coordinator", Deps: map[string]any{"protocol": "protocol", "builder": "builder", "chain": "store", "states": "store", "executor": "executor", "committer": "store", "pool": "pool"}}}
}
func defaultTransaction(t *testing.T, nonce uint64) contracts.Transaction {
	t.Helper()
	raw := types.Transaction{ChainID: "dev", Nonce: nonce, Key: []byte("hello"), Value: []byte("world")}
	if err := raw.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}
	tx, err := kv.WrapTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
func TestProductionGraph(t *testing.T) {
	ctx := context.Background()
	graph := productionGraph(config.PluginUse{Use: "storage/memory"}, false)
	host, result, err := build.Build[contracts.ConsensusHost](ctx, graph, "host")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Instances) != 6 {
		t.Fatalf("duplicated shared dependencies: %d", len(result.Instances))
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
	raw, err := kv.UnwrapBlock(next)
	if err != nil || raw.Header.Height != 2 {
		t.Fatal("head not advanced", err)
	}
}
func TestPersistentProductionGraph(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "default"
		if custom {
			name = "custom-format"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "chain.db")
			graph := productionGraph(config.PluginUse{Use: "storage/bolt", Config: map[string]any{"path": path}}, custom)
			var confirmed contracts.ID
			var previous contracts.ID
			for phase := 0; phase < 2; phase++ {
				host, result, err := build.Build[contracts.ConsensusHost](ctx, graph, "host")
				if err != nil {
					t.Fatal(err)
				}
				lifecycle, err := build.RequireByID[contracts.Lifecycle](result, "store", "store")
				if err != nil {
					t.Fatal(err)
				}
				if err := lifecycle.Start(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lifecycle.Stop(context.Background()) })
				pool, err := build.RequireByID[contracts.Mempool](result, "pool", "pool")
				if err != nil {
					t.Fatal(err)
				}
				transactions, err := build.RequireByID[contracts.TransactionStore](result, "store", "store")
				if err != nil {
					t.Fatal(err)
				}
				chain, err := build.RequireByID[contracts.ChainStore](result, "store", "store")
				if err != nil {
					t.Fatal(err)
				}
				if phase == 1 {
					stored, err := transactions.Transaction(ctx, confirmed)
					if err != nil || stored.BlockID != previous || stored.Transaction.ID() != confirmed {
						t.Fatal("transaction not recovered", err)
					}
					head, err := chain.Head(ctx)
					if err != nil || head != previous {
						t.Fatal("head not recovered", err)
					}
				}
				var tx contracts.Transaction
				if custom {
					tx = testprotocol.Tx{Label: string(rune('a' + phase)), Body: []byte{0, 0xff, byte(phase)}}
					if len(tx.ID()) == 32 {
						t.Fatal("custom ID accidentally fixed width")
					}
				} else {
					tx = defaultTransaction(t, uint64(phase+1))
				}
				if err := pool.Add(ctx, tx); err != nil {
					t.Fatal(err)
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
				confirmed = tx.ID()
				previous = block.ID()
				if custom {
					frame := block.(testprotocol.Frame)
					if frame.Revision != uint64(phase+1) || len(frame.OtherLinks) != 2 || len(frame.Commitment) != 64 {
						t.Fatal("custom layout was constrained")
					}
					states, err := build.RequireByID[contracts.StateStore](result, "store", "store")
					if err != nil {
						t.Fatal(err)
					}
					snapshot, err := states.Snapshot(ctx, block.ID())
					if err != nil {
						t.Fatal(err)
					}
					body, err := snapshot.Get(ctx, []byte(string(rune('a'+phase))))
					_ = snapshot.Close()
					if err != nil || len(body) != 3 || body[1] != 0xff {
						t.Fatal("custom state missing", err)
					}
				}
				if err := lifecycle.Stop(ctx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
