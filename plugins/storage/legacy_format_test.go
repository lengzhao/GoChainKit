package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lengzhao/GoChainKit/internal/testbackend"
	bolt "go.etcd.io/bbolt"
)

// Build a pre-backend format-2 file directly with bbolt rather than using KVStore.
// Namespace/gob layout must remain readable after the storage abstraction changes.
func TestLegacyFormatTwoOpens(t *testing.T) {
	ctx := context.Background()
	p := mustProtocol("dev")
	g, e := prepareGenesis(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "legacy.db")
	d, e := bolt.Open(path, 0600, nil)
	if e != nil {
		t.Fatal(e)
	}
	e = d.Update(func(tx *bolt.Tx) error {
		for _, n := range []string{"meta", "blocks", "states", "transactions", "receipts", "roots", "results"} {
			if _, e := tx.CreateBucket([]byte(n)); e != nil {
				return e
			}
		}
		if e := tx.Bucket([]byte("meta")).Put([]byte("format"), []byte("2")); e != nil {
			return e
		}
		for _, v := range []struct {
			n, k string
			v    any
		}{{"meta", "genesisID", g.ID}, {"meta", "genesisRoot", g.Root}, {"meta", "genesisBytes", g.Block}, {"meta", "protocol", p.Protocol()}, {"meta", "head", g.ID}, {"blocks", string(g.ID), g.Block}, {"states", string(g.ID), g.State}, {"roots", string(g.ID), g.Root}} {
			data, e := encode(v.v)
			if e != nil {
				return e
			}
			if e = tx.Bucket([]byte(v.n)).Put([]byte(v.k), data); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := NewKV(KVConfig{}, KVDeps{Protocol: p, Backend: testbackend.Bolt(t, path)})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer s.Stop(ctx)
	if h, e := s.Head(ctx); e != nil || h != g.ID {
		t.Fatal(h, e)
	}
	parent, b, r := candidate(t, s)
	if e = s.Commit(ctx, parent, b, r); e != nil {
		t.Fatal(e)
	}
}
