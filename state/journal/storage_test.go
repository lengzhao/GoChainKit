package journal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/GoChainKit/state/journal"
)

func TestRealSnapshotRemainsBorrowedAndUnchanged(t *testing.T) {
	ctx := context.Background()
	p, e := kv.New(kv.Config{ChainID: "journal"})
	if e != nil {
		t.Fatal(e)
	}
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	head, _ := ledger.Head(ctx)
	snap, e := ledger.Snapshot(ctx, head)
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	baseline, e := journal.FromStateReader(snap)
	if e != nil {
		t.Fatal(e)
	}
	j := create(t, factory(t, journal.Config{}), baseline)
	if _, ok, e := j.Get(ctx, "local"); e != nil || ok {
		t.Fatal(ok, e)
	}
	if e = j.Set(ctx, "local", []byte("isolated")); e != nil {
		t.Fatal(e)
	}
	if _, e = snap.Get(ctx, []byte("local")); !errors.Is(e, core.ErrNotFound) {
		t.Fatal("journal modified snapshot", e)
	}
	if _, e = snap.Entries(ctx); e != nil {
		t.Fatal("journal closed borrowed snapshot", e)
	}
}
