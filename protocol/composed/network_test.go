package composed_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	identity "github.com/lengzhao/GoChainKit/identity/libp2p"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	runtime "github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/consensus"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/network"
	"github.com/lengzhao/GoChainKit/plugins/replication"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type composedNode struct {
	ledger  *storage.KVStore
	pool    core.Mempool
	builder core.BlockBuilder
	network *network.Transport
	cancel  context.CancelFunc
	done    chan error
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("composed network condition timed out")
}
func startNode(t *testing.T, p *composed.WorkProtocol, path string) *composedNode {
	t.Helper()
	s, e := storage.NewKV(storage.KVConfig{}, storage.KVDeps{Backend: testbackend.Bolt(t, filepath.Join(path, "chain.db")), Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 100, MaxBytes: 2 << 20}, mempool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	executor, e := composed.NewExecutor(p, s)
	if e != nil {
		t.Fatal(e)
	}
	selector, e := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	builder, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 1 << 20}, builder.Deps{Protocol: p, Ledger: s, Pool: pool, Executor: executor, Selector: selector})
	if e != nil {
		t.Fatal(e)
	}
	network, e := network.NewLibp2p(network.Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}, Timeout: "3s"}, network.Deps{Protocol: p, Identity: newPeerIdentity()})
	if e != nil {
		t.Fatal(e)
	}
	host, e := runtime.NewCoordinator(runtime.CoordinatorDeps{Protocol: p, Ledger: s, Pool: pool, Builder: builder, Executor: executor})
	if e != nil {
		t.Fatal(e)
	}
	mining := false
	imp, e := replication.NewImporter(replication.ImportConfig{}, replication.ImportDeps{Rules: p, Ledger: s, Executor: executor, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	rep, e := replication.NewSync(replication.SyncConfig{Interval: "20ms"}, replication.SyncDeps{Rules: p, Ledger: s, Pool: pool, Network: network, Importer: imp})
	if e != nil {
		t.Fatal(e)
	}
	consensus, e := consensus.NewPoW(consensus.PoWConfig{Interval: "20ms", Mining: &mining}, consensus.PoWDeps{Protocol: p, Ledger: s, Builder: builder, Importer: imp, Replication: rep})
	if e != nil {
		t.Fatal(e)
	}
	runner, e := runtime.NewRunner(runtime.RunnerConfig{ShutdownTimeout: "5s"}, runtime.RunnerDeps{Ledger: s, Host: host, Consensus: consensus, Resources: []core.Lifecycle{network}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &composedNode{s, pool, builder, network, cancel, make(chan error, 1)}
	go func() { n.done <- runner.Run(ctx) }()
	eventually(t, func() bool { return len(network.Addresses()) > 0 })
	t.Cleanup(func() { n.stop(t) })
	return n
}
func (n *composedNode) stop(t *testing.T) {
	t.Helper()
	if n.cancel == nil {
		return
	}
	n.cancel()
	select {
	case e := <-n.done:
		if e != nil {
			t.Errorf("composed shutdown: %v", e)
		}
	case <-time.After(6 * time.Second):
		t.Error("composed shutdown timed out")
	}
	n.cancel = nil
}
func TestComposedAccountUTXORealNetworkAndRestart(t *testing.T) {
	for _, kind := range []string{"account", "utxo", "counter"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{6}, 32))
			owner := asset.AddressOf(key[32:])
			to := assetcap.Address{7}
			miner := assetcap.Address{8}
			var engine model.Engine
			var tx core.Transaction
			if kind == "account" {
				m, _ := account.New(account.Config{Network: "real-" + kind, Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
				engine = m
				raw := account.Transaction{Network: "real-" + kind, Nonce: 1, To: to, Amount: 30, Fee: 2}
				if e := raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, _ = account.Wrap(raw)
			} else if kind == "utxo" {
				m, _ := utxo.New(utxo.Config{Network: "real-" + kind, Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
				engine = m
				raw := utxo.Transaction{Network: "real-" + kind, Inputs: []utxo.Input{{Previous: m.GenesisOutpoints()[0]}}, Outputs: []utxo.Output{{To: to, Amount: 30}, {To: owner, Amount: 68}}}
				if e := raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, _ = utxo.Wrap(raw)
			} else {
				engine = identityCounter{counterModel{testprotocol.New("real-" + kind)}}
				tx = testprotocol.Tx{Label: "increment"}
			}
			reward := uint64(50)
			if kind == "counter" {
				reward = 0
			}
			rule, _ := pow.New(pow.Config{RetargetInterval: 100, TargetSpacing: 10, InitialReward: reward})
			p, e := composed.NewPoW(composed.Config{Network: "real-" + kind, GenesisTime: 1700000000, Producer: miner[:]}, engine, rule, rule)
			if e != nil {
				t.Fatal(e)
			}
			root := t.TempDir()
			nodes := []*composedNode{startNode(t, p, filepath.Join(root, "0")), startNode(t, p, filepath.Join(root, "1"))}
			connect := func() {
				if e := nodes[0].network.Connect(ctx, nodes[1].network.Addresses()[0]); e != nil {
					t.Fatal(e)
				}
			}
			connect()
			if e = nodes[0].pool.Add(ctx, tx); e != nil {
				t.Fatal(e)
			}
			eventually(t, func() bool { txs, _ := nodes[1].pool.Candidates(ctx); return len(txs) == 1 })
			mine := func(n *composedNode) {
				parent, _ := n.ledger.Head(ctx)
				b, r, e := n.builder.Build(ctx, parent)
				if e != nil {
					t.Fatal(e)
				}
				b, e = p.Seal(ctx, b)
				if e != nil {
					t.Fatal(e)
				}
				r.BlockID = b.ID()
				if _, e = n.ledger.Import(ctx, parent, b, r); e != nil {
					t.Fatal(e)
				}
				txs, _ := p.Transactions(b)
				ids := make([]core.ID, len(txs))
				for i, tx := range txs {
					ids[i] = tx.ID()
				}
				if e = n.pool.Remove(ctx, ids); e != nil {
					t.Fatal(e)
				}
			}
			mine(nodes[1])
			eventually(t, func() bool { _, e := nodes[0].ledger.Transaction(ctx, tx.ID()); return e == nil })
			nodes[0].stop(t)
			mine(nodes[1])
			mine(nodes[1])
			nodes[0] = startNode(t, p, filepath.Join(root, "0"))
			connect()
			eventually(t, func() bool { a, _ := nodes[0].ledger.Head(ctx); b, _ := nodes[1].ledger.Head(ctx); return a == b })
			var entries map[string][]byte
			var rootHash core.Commitment
			for i, n := range nodes {
				head, _ := n.ledger.Head(ctx)
				snap, e := n.ledger.Snapshot(ctx, head)
				if e != nil {
					t.Fatal(e)
				}
				state, _ := snap.Entries(ctx)
				if i == 0 {
					entries = state
					rootHash = snap.Root()
				} else if rootHash != snap.Root() || !reflect.DeepEqual(entries, state) {
					t.Fatal("network state divergence")
				}
				snap.Close()
				if _, e = n.ledger.Transaction(ctx, tx.ID()); e != nil {
					t.Fatal(fmt.Errorf("confirmed tx lost: %w", e))
				}
			}
		})
	}
}

func newPeerIdentity() *identity.Provider {
	p, _ := identity.New(identity.Config{KeyID: "p2p"}, identity.Deps{Store: keystore.NewMemory()})
	return p
}
