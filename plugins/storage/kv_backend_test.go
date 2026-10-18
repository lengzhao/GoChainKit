package storage

import (
	"context"
	"errors"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/storage/backend"
	"path/filepath"
	"testing"
)

// The very same ledger and commit contract runs on both transactional backends.
func TestKVBackendContract(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "bolt"}[disk], func(t *testing.T) {
			var f capstorage.Backend = backend.NewMemory()
			if disk {
				f, _ = backend.NewBolt(backend.BoltConfig{Path: filepath.Join(t.TempDir(), "chain.db")})
			}
			s, e := NewKV(KVConfig{}, KVDeps{Protocol: mustProtocol("dev"), Backend: f})
			if e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			if e = s.Start(ctx); e != nil {
				t.Fatal(e)
			}
			defer s.Stop(ctx)
			parent, b, r := candidate(t, s)
			cancelctx, cancel := context.WithCancel(ctx)
			s.beforeHead = func() error { cancel(); return nil }
			e = s.Commit(cancelctx, parent, b, r)
			if !errors.Is(e, context.Canceled) || errors.Is(e, core.ErrCommitUnknown) {
				t.Fatalf("cancel rollback %v", e)
			}
			if h, _ := s.Head(ctx); h != parent {
				t.Fatal("partial head")
			}
			if _, e = s.Block(ctx, b.ID()); e != core.ErrNotFound {
				t.Fatalf("partial block %v", e)
			}
			s.beforeHead = nil
			if e = s.Commit(ctx, parent, b, r); e != nil {
				t.Fatal(e)
			}
			if h, _ := s.Head(ctx); h != b.ID() {
				t.Fatal("head")
			}
			if _, e = s.Snapshot(ctx, b.ID()); e != nil {
				t.Fatal(e)
			}
			for _, receipt := range r.Receipts {
				c, e := s.Confirmation(ctx, receipt.TransactionID)
				if e != nil || c.Head != b.ID() || c.Receipt == nil {
					t.Fatal(c, e)
				}
			}
		})
	}
}

type failingReader struct {
	capstorage.Reader
	err error
}

func (r failingReader) Get(string, []byte) ([]byte, error) { return nil, r.err }
func TestReadFailureRetainsMixedCause(t *testing.T) {
	fault := errors.New("disk failure")
	mixed := errors.Join(capstorage.ErrNotFound, fault)
	var value string
	e := get(failingReader{err: mixed}, "meta", []byte("x"), &value)
	if !errors.Is(e, fault) || errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
}
