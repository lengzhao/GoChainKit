package integration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	runtime "github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/consensus"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/network"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	store "github.com/lengzhao/GoChainKit/plugins/storage"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type powTestNode struct {
	ledger  *store.BoltStore
	pool    core.Mempool
	builder core.BlockBuilder
	network *network.Transport
	cancel  context.CancelFunc
	done    chan error
}

func startPoWNode(t *testing.T, p core.WorkProtocol, path string) *powTestNode {
	return startPoWNodeMode(t, p, path, false)
}
func startPoWNodeMode(t *testing.T, p core.WorkProtocol, path string, mining bool) *powTestNode {
	t.Helper()
	s, e := store.NewBolt(store.BoltConfig{Path: filepath.Join(path, "chain.db")}, store.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	m, e := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 100, MaxBytes: 4 << 20}, pool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	ex, e := pow.NewExecutor(p)
	if e != nil {
		t.Fatal(e)
	}
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 1536 << 10}, builder.Deps{Protocol: p, Ledger: s, Pool: m, Executor: ex, Selector: sel})
	if e != nil {
		t.Fatal(e)
	}
	n, e := network.NewLibp2p(network.Config{IdentityPath: filepath.Join(path, "peer.key"), Listen: []string{"/ip4/127.0.0.1/tcp/0"}, Timeout: "10s"}, network.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	host, e := runtime.NewCoordinator(runtime.CoordinatorDeps{Protocol: p, Ledger: s, Builder: b, Executor: ex, Pool: m})
	if e != nil {
		t.Fatal(e)
	}
	c, e := consensus.NewPoW(consensus.PoWConfig{Interval: "25ms", SyncInterval: "20ms", Mining: &mining}, consensus.PoWDeps{Protocol: p, Ledger: s, Builder: b, Executor: ex, Pool: m, Network: n})
	if e != nil {
		t.Fatal(e)
	}
	runner, e := runtime.NewRunner(runtime.RunnerConfig{ShutdownTimeout: "5s"}, runtime.RunnerDeps{Ledger: s, Host: host, Consensus: c, Resources: []core.Lifecycle{n}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := &powTestNode{s, m, b, n, cancel, make(chan error, 1)}
	go func() { out.done <- runner.Run(ctx) }()
	eventuallyPoW(t, func() bool { return len(n.Addresses()) > 0 })
	t.Cleanup(func() { out.stop(t) })
	return out
}
func (n *powTestNode) stop(t *testing.T) {
	t.Helper()
	if n.cancel == nil {
		return
	}
	n.cancel()
	select {
	case e := <-n.done:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Errorf("node stop: %v", e)
		}
	case <-time.After(8 * time.Second):
		t.Error("node shutdown timed out")
	}
	n.cancel = nil
}
func eventuallyPoW(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("PoW network condition timed out")
}
func signedPoW(t *testing.T, p core.WorkProtocol, sender byte, value string) core.Transaction {
	t.Helper()
	seed := make([]byte, 32)
	seed[0] = sender
	tx := pow.Transaction{ChainID: p.Protocol().Network, Nonce: 1, Key: []byte("k"), Value: []byte(value)}
	if e := tx.Sign(ed25519.NewKeyFromSeed(seed)); e != nil {
		t.Fatal(e)
	}
	w, e := pow.WrapTransaction(tx)
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func minePoW(t *testing.T, n *powTestNode, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		head, e := n.ledger.Head(ctx)
		if e != nil {
			t.Fatal(e)
		}
		b, r, e := n.builder.Build(ctx, head)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = n.ledger.Import(ctx, head, b, r); e != nil {
			t.Fatal(e)
		}
		var ids []core.ID
		for _, receipt := range r.Receipts {
			ids = append(ids, receipt.TransactionID)
		}
		if e = n.pool.Remove(ctx, ids); e != nil {
			t.Fatal(e)
		}
	}
}
func TestPoWThreeNodesReorgRestartAndPagedCatchup(t *testing.T) {
	p, e := pow.New(pow.Config{ChainID: "three-nodes"})
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	a := startPoWNode(t, p, filepath.Join(dir, "a"))
	b := startPoWNode(t, p, filepath.Join(dir, "b"))
	c := startPoWNode(t, p, filepath.Join(dir, "c"))
	ctx := context.Background()
	canonical := signedPoW(t, p, 1, "canonical")
	detached := signedPoW(t, p, 2, "detached")
	a.pool.Add(ctx, canonical)
	b.pool.Add(ctx, detached)
	minePoW(t, b, 35)
	oldB, _ := b.ledger.Head(ctx)
	minePoW(t, a, 70)
	target, _ := a.ledger.Head(ctx)
	if e = b.network.Connect(ctx, a.network.Addresses()[0]); e != nil {
		t.Fatal(e)
	}
	eventuallyPoW(t, func() bool { h, _ := b.ledger.Head(ctx); return h == target })
	if _, e = b.ledger.Block(ctx, oldB); e != nil {
		t.Fatal("detached fork not retained", e)
	}
	if _, e = b.ledger.Transaction(ctx, detached.ID()); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("detached index retained", e)
	}
	eventuallyPoW(t, func() bool {
		txs, _ := b.pool.Candidates(ctx)
		for _, tx := range txs {
			if tx.ID() == detached.ID() {
				return true
			}
		}
		return false
	})
	if e = c.network.Connect(ctx, b.network.Addresses()[0]); e != nil {
		t.Fatal(e)
	}
	eventuallyPoW(t, func() bool { h, _ := c.ledger.Head(ctx); return h == target })
	assertPoWStateEqual(t, a, b, c)
	for i, n := range []*powTestNode{a, b, c} {
		if _, e = n.ledger.Transaction(ctx, canonical.ID()); e != nil {
			t.Fatal(fmt.Sprintf("node %d missing canonical tx", i), e)
		}
	}
	b.stop(t)
	minePoW(t, a, 3)
	target, _ = a.ledger.Head(ctx)
	b = startPoWNode(t, p, filepath.Join(dir, "b"))
	if e = b.network.Connect(ctx, a.network.Addresses()[0]); e != nil {
		t.Fatal(e)
	}
	eventuallyPoW(t, func() bool { h, _ := b.ledger.Head(ctx); return h == target })
	if _, e = b.ledger.Block(ctx, oldB); e != nil {
		t.Fatal("side branch lost after restart", e)
	}
}

func TestPoWThreeAutomaticMinersConvergeAfterRestart(t *testing.T) {
	p, _ := pow.New(pow.Config{ChainID: "automatic-miners"})
	dir := t.TempDir()
	nodes := make([]*powTestNode, 3)
	for i := range nodes {
		nodes[i] = startPoWNodeMode(t, p, filepath.Join(dir, fmt.Sprint(i)), true)
	}
	ctx := context.Background()
	height := func(n *powTestNode) uint64 {
		h, e := n.ledger.Head(ctx)
		if e != nil {
			return 0
		}
		b, e := n.ledger.Block(ctx, h)
		if e != nil {
			return 0
		}
		v, e := pow.UnwrapBlock(b)
		if e != nil {
			return 0
		}
		return v.Header.Height
	}
	for _, n := range nodes {
		eventuallyPoW(t, func() bool { return height(n) >= 3 })
	}
	for i, n := range nodes {
		if e := n.network.Connect(ctx, nodes[(i+1)%3].network.Addresses()[0]); e != nil {
			t.Fatal(e)
		}
	}
	tx := signedPoW(t, p, 7, "propagated")
	if e := nodes[0].pool.Add(ctx, tx); e != nil {
		t.Fatal(e)
	}
	eventuallyPoW(t, func() bool { _, e := nodes[1].ledger.Transaction(ctx, tx.ID()); return e == nil })
	for _, n := range nodes {
		n.stop(t)
	}
	for i := range nodes {
		nodes[i] = startPoWNodeMode(t, p, filepath.Join(dir, fmt.Sprint(i)), false)
	}
	// Strictly heavier selection deliberately keeps equal-work local tips. Add
	// one settling block to a maximal-work node before pausing every miner.
	max := nodes[0]
	for _, n := range nodes[1:] {
		if height(n) > height(max) {
			max = n
		}
	}
	minePoW(t, max, 1)
	for i, n := range nodes {
		if e := n.network.Connect(ctx, nodes[(i+1)%3].network.Addresses()[0]); e != nil {
			t.Fatal(e)
		}
	}
	eventuallyPoW(t, func() bool {
		a, e := nodes[0].ledger.Head(ctx)
		if e != nil {
			return false
		}
		for _, n := range nodes[1:] {
			b, e := n.ledger.Head(ctx)
			if e != nil || b != a {
				return false
			}
		}
		return true
	})
	assertPoWStateEqual(t, nodes...)
	for _, n := range nodes {
		if _, e := n.ledger.Transaction(ctx, tx.ID()); e != nil {
			t.Fatal("canonical propagated transaction missing", e)
		}
	}
}

func assertPoWStateEqual(t *testing.T, nodes ...*powTestNode) {
	t.Helper()
	ctx := context.Background()
	head, e := nodes[0].ledger.Head(ctx)
	if e != nil {
		t.Fatal(e)
	}
	first, e := nodes[0].ledger.Snapshot(ctx, head)
	if e != nil {
		t.Fatal(e)
	}
	entries, e := first.Entries(ctx)
	root := first.Root()
	first.Close()
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range nodes[1:] {
		h, e := n.ledger.Head(ctx)
		if e != nil || h != head {
			t.Fatal("heads differ", e)
		}
		snap, e := n.ledger.Snapshot(ctx, h)
		if e != nil {
			t.Fatal(e)
		}
		other, e := snap.Entries(ctx)
		sameRoot := snap.Root() == root
		snap.Close()
		if e != nil || !sameRoot || !reflect.DeepEqual(entries, other) {
			t.Fatal("snapshot contents or root diverged", e)
		}
	}
}
func TestPoWLargeTransactionRealPropagation(t *testing.T) {
	p, _ := pow.New(pow.Config{ChainID: "large-gossip"})
	dir := t.TempDir()
	a := startPoWNode(t, p, filepath.Join(dir, "a"))
	b := startPoWNode(t, p, filepath.Join(dir, "b"))
	ctx := context.Background()
	if e := a.network.Connect(ctx, b.network.Addresses()[0]); e != nil {
		t.Fatal(e)
	}
	raw := pow.Transaction{ChainID: "large-gossip", Nonce: 1, Key: []byte("large"), Value: make([]byte, 1<<20)}
	if e := raw.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); e != nil {
		t.Fatal(e)
	}
	tx, e := pow.WrapTransaction(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.pool.Add(ctx, tx); e != nil {
		t.Fatal(e)
	}
	small := signedPoW(t, p, 8, "small")
	if e = a.pool.Add(ctx, small); e != nil {
		t.Fatal(e)
	}
	eventuallyPoW(t, func() bool {
		txs, _ := b.pool.Candidates(ctx)
		found := map[core.ID]bool{}
		for _, v := range txs {
			found[v.ID()] = true
		}
		return found[tx.ID()] && found[small.ID()]
	})
	minePoW(t, b, 1)
	eventuallyPoW(t, func() bool { _, e := a.ledger.Transaction(ctx, tx.ID()); return e == nil })
	assertPoWStateEqual(t, a, b)
}
