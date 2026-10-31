package storage_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/lengzhao/GoChainKit/archive/transactions"
	"github.com/lengzhao/GoChainKit/body/materializer"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"path/filepath"
	"testing"
)

type switchSource struct {
	bodycap.TransactionArchive
	missing bool
	failure error
}

func (s *switchSource) Put(ctx context.Context, tx core.Transaction) error {
	if s.failure != nil {
		return s.failure
	}
	return s.TransactionArchive.Put(ctx, tx)
}
func (s *switchSource) Transaction(ctx context.Context, id core.ID) (core.Transaction, error) {
	if s.missing {
		return nil, core.ErrNotFound
	}
	return s.TransactionArchive.Transaction(ctx, id)
}
func TestReferenceLedgerArchiveReopenAndMissing(t *testing.T) {
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	m, _ := account.New(account.Config{Network: "ref-ledger", Allocations: []assetcap.Allocation{{Address: asset.AddressOf(key[32:]), Amount: 100}}})
	rule, _ := pow.New(pow.Config{})
	p, _ := composed.NewPoW(composed.Config{Network: "ref-ledger", GenesisTime: 1700000000, ReferenceBody: true}, m, rule, rule)
	if _, e := storage.NewKV(storage.KVConfig{}, storage.KVDeps{Protocol: p, Backend: testbackend.Bolt(t, filepath.Join(t.TempDir(), "bad.db"))}); e == nil {
		t.Fatal("missing body dependencies accepted")
	}
	dir := t.TempDir()
	ad := transactions.Deps{Protocol: p, Backend: testbackend.Bolt(t, filepath.Join(dir, "archive.db"))}
	a, _ := transactions.New(transactions.Config{}, ad)
	if e := a.Start(ctx); e != nil {
		t.Fatal(e)
	}
	source := &switchSource{TransactionArchive: a}
	local, _ := materializer.New(materializer.Config{}, materializer.Deps{References: p, Protocol: p, Sources: []bodycap.TransactionSource{source}})
	d := storage.KVDeps{Protocol: p, Backend: testbackend.Bolt(t, filepath.Join(dir, "chain.db")), Archive: source, Materializer: local}
	s, e := storage.NewKV(storage.KVConfig{}, d)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	head, _ := s.Head(ctx)
	parent, _ := s.Block(ctx, head)
	raw := account.Transaction{Network: "ref-ledger", Nonce: 1, Amount: 7, To: assetcap.Address{2}}
	raw.Sign(key)
	tx, _ := account.Wrap(raw)
	draft, e := p.Draft(ctx, parent, []core.Transaction{tx})
	if e != nil {
		t.Fatal(e)
	}
	ex, _ := composed.NewExecutor(p, s)
	snap, _ := s.Snapshot(ctx, head)
	r, e := ex.Execute(ctx, snap, draft)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.Finalize(ctx, draft, r)
	if e != nil {
		t.Fatal(e)
	}
	b, e = p.Seal(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	r.BlockID = b.ID()
	disk := errors.New("archive write fault")
	source.failure = disk
	if _, err := s.Import(ctx, head, b, r); !errors.Is(err, disk) {
		t.Fatal("archive failure lost", err)
	}
	if current, err := s.Head(ctx); err != nil || current != head {
		t.Fatal("head advanced after archive failure", current, err)
	}
	source.failure = nil
	if _, e = s.Import(ctx, head, b, r); e != nil {
		t.Fatal(e)
	}
	// A valid side branch must retain its own body even when it is not canonical.
	sideRaw := account.Transaction{Network: "ref-ledger", Nonce: 1, Amount: 8, To: assetcap.Address{3}}
	sideRaw.Sign(key)
	sideTx, _ := account.Wrap(sideRaw)
	sdraft, e := p.Draft(ctx, parent, []core.Transaction{sideTx})
	if e != nil {
		t.Fatal(e)
	}
	ssnap, _ := s.Snapshot(ctx, head)
	sr, e := ex.Execute(ctx, ssnap, sdraft)
	ssnap.Close()
	if e != nil {
		t.Fatal(e)
	}
	side, e := p.Finalize(ctx, sdraft, sr)
	if e != nil {
		t.Fatal(e)
	}
	side, e = p.Seal(ctx, side)
	if e != nil {
		t.Fatal(e)
	}
	sr.BlockID = side.ID()
	if _, e = s.Import(ctx, head, side, sr); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Transaction(ctx, sideTx.ID()); e != nil {
		t.Fatal("side body not archived", e)
	}
	if _, e = a.Transaction(ctx, tx.ID()); e != nil {
		t.Fatal("not archived", e)
	}
	source.missing = true
	if _, e = s.Block(ctx, b.ID()); !errors.Is(e, bodycap.ErrUnavailable) {
		t.Fatal("stored block missing body misclassified", e)
	}
	source.missing = false
	if e = s.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	a, _ = transactions.New(transactions.Config{}, ad)
	if e = a.Start(ctx); e != nil {
		t.Fatal(e)
	}
	local, _ = materializer.New(materializer.Config{}, materializer.Deps{References: p, Protocol: p, Sources: []bodycap.TransactionSource{a}})
	d.Archive = a
	d.Materializer = local
	s, _ = storage.NewKV(storage.KVConfig{}, d)
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := s.Block(ctx, b.ID())
	if e != nil {
		t.Fatal(e)
	}
	txs, e := p.Transactions(got)
	if e != nil || len(txs) != 1 {
		t.Fatal(txs, e)
	}
	sideRead, e := s.Block(ctx, side.ID())
	if e != nil {
		t.Fatal(e)
	}
	if stxs, e := p.Transactions(sideRead); e != nil || len(stxs) != 1 || stxs[0].ID() != sideTx.ID() {
		t.Fatal("side lost", e)
	}
	s.Stop(ctx)
	a.Stop(ctx)
	empty, _ := transactions.New(transactions.Config{}, transactions.Deps{Protocol: p, Backend: testbackend.Bolt(t, filepath.Join(dir, "empty.db"))})
	empty.Start(ctx)
	defer empty.Stop(ctx)
	local, _ = materializer.New(materializer.Config{}, materializer.Deps{References: p, Protocol: p, Sources: []bodycap.TransactionSource{empty}})
	d.Archive = empty
	d.Materializer = local
	s, _ = storage.NewKV(storage.KVConfig{}, d)
	if e = s.Start(ctx); e == nil {
		s.Stop(ctx)
		t.Fatal("missing body recovery succeeded")
	}
}
