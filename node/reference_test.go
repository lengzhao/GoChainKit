package node_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"github.com/lengzhao/GoChainKit/archive/transactions"
	"github.com/lengzhao/GoChainKit/body/materializer"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"github.com/lengzhao/GoChainKit/storage/backend"
	"testing"
)

func TestReferenceCoordinatorFreezesAndCommits(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
	engine, e := account.New(account.Config{Network: "host-ref", Allocations: []assetcap.Allocation{{Address: asset.AddressOf(key[32:]), Amount: 10}}})
	if e != nil {
		t.Fatal(e)
	}
	rule, _ := pow.New(pow.Config{})
	p, e := composed.NewPoW(composed.Config{Network: "host-ref", GenesisTime: 1700000000, ReferenceBody: true}, engine, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	archive, _ := transactions.New(transactions.Config{}, transactions.Deps{Protocol: p, Backend: backend.NewMemory()})
	if e = archive.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer archive.Stop(ctx)
	local, _ := materializer.New(materializer.Config{}, materializer.Deps{Protocol: p, References: p, Sources: []bodycap.TransactionSource{archive}})
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p, Archive: archive, Materializer: local})
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 1 << 20}, mempool.Deps{Protocol: p})
	ex, _ := composed.NewExecutor(p, ledger)
	sel, _ := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	build, _ := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 1 << 20}, builder.Deps{Protocol: p, Pool: pool, Ledger: ledger, Executor: ex, Selector: sel})
	host, e := node.NewCoordinator(node.CoordinatorDeps{Protocol: p, Ledger: ledger, Pool: pool, Builder: build, Executor: ex, Materializer: local})
	if e != nil {
		t.Fatal(e)
	}
	raw := account.Transaction{Network: "host-ref", Nonce: 1, Amount: 3, To: assetcap.Address{3}}
	raw.Sign(key)
	tx, _ := account.Wrap(raw)
	if e = pool.Add(ctx, tx); e != nil {
		t.Fatal(e)
	}
	b, e := host.Propose(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if txs, e := p.Transactions(b); e != nil || len(txs) != 1 {
		t.Fatal("freeze lost bodies", e)
	}
	b, e = p.Seal(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	if e = host.Validate(ctx, b); e != nil {
		t.Fatal(e)
	}
	if e = host.Commit(ctx, b); e != nil {
		t.Fatal(e)
	}
	head, _ := ledger.Head(ctx)
	if head != b.ID() {
		t.Fatal("not committed")
	}
	if _, e = archive.Transaction(ctx, tx.ID()); e != nil {
		t.Fatal(e)
	}
}
