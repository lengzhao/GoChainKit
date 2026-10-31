package transactions

import (
	"context"
	"errors"
	storagecap "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/storage/backend"
	"path/filepath"
	"testing"
	"time"
)

func TestArchiveDurabilityQuotaAndBinding(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("archive")
	be, e := backend.NewBolt(backend.BoltConfig{Path: filepath.Join(t.TempDir(), "archive.db")})
	if e != nil {
		t.Fatal(e)
	}
	tx := testprotocol.Tx{Label: "one", Body: []byte("value")}
	raw, _ := p.EncodeTransaction(tx)
	c := Config{MaxBytes: int64(len(raw)), MaxTransactions: 1}
	d := Deps{Protocol: p, Backend: be}
	a, e := New(c, d)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Put(ctx, tx); !errors.Is(e, core.ErrClosed) {
		t.Fatal("lazy opened", e)
	}
	if e = a.Start(ctx); e != nil {
		t.Fatal(e)
	}
	if e = a.Put(ctx, tx); e != nil {
		t.Fatal(e)
	}
	if e = a.Put(ctx, tx); e != nil {
		t.Fatal("not idempotent", e)
	}
	if e = a.Put(ctx, testprotocol.Tx{Label: "two"}); !errors.Is(e, core.ErrFull) {
		t.Fatal("quota", e)
	}
	if e = a.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	a, _ = New(c, d)
	if e = a.Start(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := a.Transaction(ctx, tx.ID())
	if e != nil || got.ID() != tx.ID() {
		t.Fatal(got, e)
	}
	a.Stop(ctx)
	d.Protocol = testprotocol.New("foreign")
	a, _ = New(c, d)
	if e = a.Start(ctx); e == nil {
		a.Stop(ctx)
		t.Fatal("foreign opened")
	}
}

type genesisVariant struct {
	*testprotocol.Format
	salt string
}

func (p genesisVariant) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	b, s, e := p.Format.Genesis(ctx)
	v := b.(testprotocol.Frame)
	v.OtherLinks = []string{p.salt}
	return v, s, e
}
func TestArchiveRejectsDifferentGenesisWithSameDescriptor(t *testing.T) {
	ctx := context.Background()
	be, _ := backend.NewBolt(backend.BoltConfig{Path: filepath.Join(t.TempDir(), "a.db")})
	p := testprotocol.New("same")
	a, _ := New(Config{}, Deps{Protocol: genesisVariant{p, "first"}, Backend: be})
	if e := a.Start(ctx); e != nil {
		t.Fatal(e)
	}
	a.Stop(ctx)
	a, _ = New(Config{}, Deps{Protocol: genesisVariant{p, "second"}, Backend: be})
	if e := a.Start(ctx); e == nil {
		a.Stop(ctx)
		t.Fatal("different genesis accepted")
	}
}

type waitingBackend struct{ entered chan struct{} }

func (b waitingBackend) Open(ctx context.Context) (storagecap.Database, error) {
	close(b.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestStopCancelsPendingArchiveStart(t *testing.T) {
	entered := make(chan struct{})
	a, _ := New(Config{}, Deps{Protocol: testprotocol.New("cancel-start"), Backend: waitingBackend{entered}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan error, 1)
	go func() { started <- a.Start(ctx) }()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop(context.Background()) }()
	select {
	case e := <-stopped:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-started
		<-stopped
		t.Fatal("stop did not cancel startup")
	}
	if e := <-started; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
