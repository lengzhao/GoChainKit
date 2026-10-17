package integration

import (
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	_ "github.com/lengzhao/GoChainKit/plugins/builder"
	_ "github.com/lengzhao/GoChainKit/plugins/executor"
	_ "github.com/lengzhao/GoChainKit/plugins/mempool"
	_ "github.com/lengzhao/GoChainKit/plugins/node"
	_ "github.com/lengzhao/GoChainKit/plugins/protocol"
	_ "github.com/lengzhao/GoChainKit/plugins/selector"
	_ "github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"path/filepath"
	"testing"
)

func init() {
	pluginkit.Register("protocol/integration-flex", func() (core.Protocol, error) { return testprotocol.New("flex"), nil })
	pluginkit.Register("executor/integration-flex", func(_ struct{}, d struct {
		Protocol core.Protocol `json:"protocol"`
	}) (core.Executor, error) {
		return testprotocol.Executor{P: d.Protocol}, nil
	})
}
func productionGraph(store config.PluginUse, custom bool) map[string]any {
	protocol := config.PluginUse{Use: "protocol/kv", Config: map[string]any{"chainID": "dev"}}
	executor := config.PluginUse{Use: "executor/kv", Deps: map[string]any{"protocol": "protocol"}}
	if custom {
		protocol = config.PluginUse{Use: "protocol/integration-flex"}
		executor = config.PluginUse{Use: "executor/integration-flex", Deps: map[string]any{"protocol": "protocol"}}
	}
	store.Deps = map[string]any{"protocol": "protocol"}
	return map[string]any{"protocol": protocol, "store": store, "pool": config.PluginUse{Use: "mempool/memory", Config: map[string]any{"maxTransactions": 10, "maxBytes": 16384}, Deps: map[string]any{"protocol": "protocol"}}, "executor": executor, "selector": config.PluginUse{Use: "selector/fifo", Deps: map[string]any{"protocol": "protocol"}}, "builder": config.PluginUse{Use: "builder/basic", Config: map[string]any{"maxTransactionBytes": 16384}, Deps: map[string]any{"protocol": "protocol", "pool": "pool", "ledger": "store", "selector": "selector", "executor": "executor"}}, "host": config.PluginUse{Use: "node/coordinator", Deps: map[string]any{"protocol": "protocol", "builder": "builder", "ledger": "store", "executor": "executor", "pool": "pool"}}}
}
func defaultTransaction(t *testing.T, nonce uint64) core.Transaction {
	t.Helper()
	raw := kv.Transaction{ChainID: "dev", Nonce: nonce, Key: []byte("hello"), Value: []byte("world")}
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
	host, result, err := build.Build[core.ConsensusHost](ctx, graph, "host")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Instances) != 7 {
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
			var confirmed core.ID
			var previous core.ID
			for phase := 0; phase < 2; phase++ {
				host, result, err := build.Build[core.ConsensusHost](ctx, graph, "host")
				if err != nil {
					t.Fatal(err)
				}
				lifecycle, err := build.RequireByID[core.Lifecycle](result, "store", "store")
				if err != nil {
					t.Fatal(err)
				}
				if err := lifecycle.Start(ctx); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lifecycle.Stop(context.Background()) })
				pool, err := build.RequireByID[core.Mempool](result, "pool", "pool")
				if err != nil {
					t.Fatal(err)
				}
				transactions, err := build.RequireByID[core.TransactionStore](result, "store", "store")
				if err != nil {
					t.Fatal(err)
				}
				chain, err := build.RequireByID[core.ChainStore](result, "store", "store")
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
				var tx core.Transaction
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
					states, err := build.RequireByID[core.StateStore](result, "store", "store")
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
