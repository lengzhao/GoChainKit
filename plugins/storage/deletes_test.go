package storage

import (
	"context"
	"errors"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"path/filepath"
	"testing"
)

func TestExplicitDeletionAndEmptyValue(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "bolt"}[disk], func(t *testing.T) {
			ctx := context.Background()
			p := testprotocol.New("deletes")
			path := filepath.Join(t.TempDir(), "ledger.db")
			var s core.Ledger
			open := func() core.Ledger {
				if !disk {
					v, e := NewMemory(MemoryConfig{}, Deps{Protocol: p})
					if e != nil {
						t.Fatal(e)
					}
					return v
				}
				v, e := NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				return v
			}
			s = open()
			defer func() {
				if v, ok := s.(*KVStore); ok {
					v.Stop(ctx)
				}
			}()
			commit := func(writes map[string][]byte, deletes []string) error {
				parent, _ := s.Head(ctx)
				pb, _ := s.Block(ctx, parent)
				draft, _ := p.Draft(ctx, pb, nil)
				snap, _ := s.Snapshot(ctx, parent)
				entries, _ := snap.Entries(ctx)
				snap.Close()
				for k, v := range writes {
					entries[k] = v
				}
				for _, k := range deletes {
					delete(entries, k)
				}
				root, _ := p.StateCommitment(ctx, entries)
				r := core.ExecutionResult{Parent: parent, BlockID: draft.ID(), Writes: writes, Deletes: deletes, StateRoot: root}
				b, _ := p.Finalize(ctx, draft, r)
				r.BlockID = b.ID()
				return s.Commit(ctx, parent, b, r)
			}
			if e := commit(map[string][]byte{"spent": []byte("old"), "empty": {}}, nil); e != nil {
				t.Fatal(e)
			}
			if e := commit(nil, []string{"spent"}); e != nil {
				t.Fatal(e)
			}
			if disk {
				s.(*KVStore).Stop(ctx)
				s = open()
			}
			head, _ := s.Head(ctx)
			snap, _ := s.Snapshot(ctx, head)
			defer snap.Close()
			if _, e := snap.Get(ctx, []byte("spent")); !errors.Is(e, core.ErrNotFound) {
				t.Fatal("deleted key present", e)
			}
			if v, e := snap.Get(ctx, []byte("empty")); e != nil || len(v) != 0 {
				t.Fatal("empty key lost", e)
			}
			if e := commit(map[string][]byte{"collision": {}}, []string{"collision"}); e == nil {
				t.Fatal("ambiguous write/delete accepted")
			}
		})
	}
}

func TestDeletionSurvivesBranchReorgAndRecovery(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "bolt"}[disk], func(t *testing.T) {
			ctx := context.Background()
			p := linearFormat{testprotocol.New("delete-branches")}
			policy := heightPolicy{p: p}
			path := filepath.Join(t.TempDir(), "fork.db")
			var s chaincap.BranchLedger
			if disk {
				v, e := NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				s = v
			} else {
				v, e := NewMemory(MemoryConfig{}, Deps{Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				s = v
			}
			defer func() {
				if v, ok := s.(*KVStore); ok {
					v.Stop(ctx)
				}
			}()
			build := func(parent core.ID, w map[string][]byte, d []string) core.Block {
				pb, _ := s.Block(ctx, parent)
				draft, _ := p.Draft(ctx, pb, nil)
				snap, _ := s.Snapshot(ctx, parent)
				entries, _ := snap.Entries(ctx)
				snap.Close()
				entries, e := core.ApplyStateChanges(ctx, entries, w, d)
				if e != nil {
					t.Fatal(e)
				}
				root, _ := p.StateCommitment(ctx, entries)
				r := core.ExecutionResult{Parent: parent, BlockID: draft.ID(), Writes: w, Deletes: d, StateRoot: root}
				b, e := p.Finalize(ctx, draft, r)
				if e != nil {
					t.Fatal(e)
				}
				r.BlockID = b.ID()
				if _, e = s.Import(ctx, parent, b, r); e != nil {
					t.Fatal(e)
				}
				return b
			}
			g, _ := s.Head(ctx)
			base := build(g, map[string][]byte{"spent": []byte("coin"), "empty": {}}, nil)
			deleted := build(base.ID(), nil, []string{"spent"})
			competing := build(base.ID(), map[string][]byte{"fork": []byte("b")}, nil)
			tip := build(competing.ID(), map[string][]byte{"fork": []byte("c")}, nil)
			head, _ := s.Head(ctx)
			if head != tip.ID() {
				t.Fatal("competing branch did not win")
			}
			snap, _ := s.Snapshot(ctx, head)
			if _, e := snap.Get(ctx, []byte("spent")); e != nil {
				t.Fatal("reorg did not restore input", e)
			}
			snap.Close()
			deletedTip := build(deleted.ID(), map[string][]byte{"fork": []byte("d")}, nil)
			final := build(deletedTip.ID(), map[string][]byte{"fork": []byte("e")}, nil)
			if disk {
				s.(*KVStore).Stop(ctx)
				v, e := NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p, Policy: policy})
				if e != nil {
					t.Fatal(e)
				}
				if e = v.Start(ctx); e != nil {
					t.Fatal(e)
				}
				s = v
			}
			head, _ = s.Head(ctx)
			if head != final.ID() {
				t.Fatal("deleted branch head lost")
			}
			snap, _ = s.Snapshot(ctx, head)
			defer snap.Close()
			if _, e := snap.Get(ctx, []byte("spent")); !errors.Is(e, core.ErrNotFound) {
				t.Fatal("delete lost across reorg/recovery", e)
			}
			if _, e := snap.Get(ctx, []byte("empty")); e != nil {
				t.Fatal("empty value mistaken for delete", e)
			}
		})
	}
}
