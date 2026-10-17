// Package bolt persists protocol-neutral records using synchronous transactions.
package bolt

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/runtime/ledger"
	"github.com/lengzhao/pluginkit"
	db "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var buckets = []string{"meta", "blocks", "states", "transactions", "receipts", "roots", "results"}

type Config struct {
	Path string `json:"path"`
}
type Deps struct {
	Protocol contracts.Protocol `json:"protocol"`
}
type TransactionRecord = contracts.ConfirmedTransaction
type Store struct {
	mu          sync.RWMutex
	cfg         Config
	p           contracts.Protocol
	db          *db.DB
	stopped     bool
	starting    bool
	startDone   chan struct{}
	startCancel context.CancelFunc
	beforeHead  func() error
}

func init() { pluginkit.Register("storage/bolt", New) }
func New(c Config, d Deps) (*Store, error) {
	if c.Path == "" || d.Protocol == nil {
		return nil, fmt.Errorf("path and protocol required")
	}
	if e := d.Protocol.Protocol().Validate(); e != nil {
		return nil, e
	}
	return &Store{cfg: c, p: d.Protocol}, nil
}
func (s *Store) Protocol() contracts.ProtocolDescriptor { return s.p.Protocol() }
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), e
}
func put(tx *db.Tx, b string, k []byte, v any) error {
	data, e := encode(v)
	if e != nil {
		return e
	}
	return tx.Bucket([]byte(b)).Put(k, data)
}
func get(tx *db.Tx, b string, k []byte, v any) error {
	bucket := tx.Bucket([]byte(b))
	if bucket == nil {
		return fmt.Errorf("missing bucket")
	}
	data := bucket.Get(k)
	if data == nil {
		return contracts.ErrNotFound
	}
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
func (s *Store) Start(ctx context.Context) (err error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return contracts.ErrClosed
	}
	if s.db != nil || s.starting {
		s.mu.Unlock()
		return fmt.Errorf("already started or starting")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.starting = true
	s.startDone = make(chan struct{})
	s.startCancel = cancel
	done := s.startDone
	s.mu.Unlock()
	var database *db.DB
	defer func() {
		s.mu.Lock()
		if err == nil && (s.stopped || ctx.Err() != nil) {
			err = contracts.ErrClosed
		}
		if err == nil {
			s.db = database
		} else if database != nil {
			database.Close()
		}
		s.starting = false
		s.startCancel = nil
		close(done)
		s.mu.Unlock()
		cancel()
	}()
	g, e := ledger.Genesis(ctx, s.p)
	if e != nil {
		return e
	}
	desc := s.p.Protocol()
	if e = os.MkdirAll(filepath.Dir(s.cfg.Path), 0700); e != nil {
		return e
	}
	database, e = db.Open(s.cfg.Path, 0600, &db.Options{Timeout: time.Second})
	if e != nil {
		return e
	}
	e = database.Update(func(tx *db.Tx) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		meta := tx.Bucket([]byte("meta"))
		if meta != nil {
			if !bytes.Equal(meta.Get([]byte("format")), []byte("2")) {
				return fmt.Errorf("unsupported storage format")
			}
			var stored contracts.ProtocolDescriptor
			if e := get(tx, "meta", []byte("protocol"), &stored); e != nil {
				return e
			}
			if stored != desc {
				return fmt.Errorf("database protocol mismatch")
			}
			var genesisID contracts.ID
			var genesisRoot contracts.Commitment
			var genesisBytes []byte
			if e := get(tx, "meta", []byte("genesisID"), &genesisID); e != nil {
				return e
			}
			if e := get(tx, "meta", []byte("genesisRoot"), &genesisRoot); e != nil {
				return e
			}
			if e := get(tx, "meta", []byte("genesisBytes"), &genesisBytes); e != nil {
				return e
			}
			if genesisID != g.ID || genesisRoot != g.Root || !bytes.Equal(genesisBytes, g.Block) {
				return fmt.Errorf("database genesis mismatch")
			}
			for _, b := range buckets {
				if tx.Bucket([]byte(b)) == nil {
					return fmt.Errorf("missing bucket")
				}
			}
			return nil
		}
		if k, _ := tx.Cursor().First(); k != nil {
			return fmt.Errorf("unrecognized database")
		}
		for _, b := range buckets {
			if _, e := tx.CreateBucket([]byte(b)); e != nil {
				return e
			}
		}
		if e := tx.Bucket([]byte("meta")).Put([]byte("format"), []byte("2")); e != nil {
			return e
		}
		for _, v := range []struct {
			b string
			k []byte
			v any
		}{{"meta", []byte("genesisID"), g.ID}, {"meta", []byte("genesisRoot"), g.Root}, {"meta", []byte("genesisBytes"), g.Block}, {"meta", []byte("protocol"), desc}, {"meta", []byte("head"), g.ID}, {"blocks", []byte(g.ID), g.Block}, {"states", []byte(g.ID), g.State}, {"roots", []byte(g.ID), g.Root}} {
			if e := put(tx, v.b, v.k, v.v); e != nil {
				return e
			}
		}
		return nil
	})

	if e != nil {
		return e
	}
	reader := &Store{p: s.p, db: database}
	head, e := reader.Head(ctx)
	if e == nil {
		_, e = reader.Block(ctx, head)
	}
	if e == nil {
		snap, err := reader.Snapshot(ctx, head)
		if err != nil {
			e = err
		} else {
			if head == g.ID && snap.Root() != g.Root {
				e = fmt.Errorf("genesis state mismatch")
			}
			snap.Close()
		}
	}
	if e == nil && head != g.ID {
		var result contracts.ExecutionResult
		e = reader.view(ctx, func(tx *db.Tx) error { return get(tx, "results", []byte(head), &result) })
		if e == nil {
			var parent, block contracts.Block
			parent, e = reader.Block(ctx, result.Parent)
			if e == nil {
				block, e = reader.Block(ctx, head)
			}
			if e == nil {
				e = s.p.ValidateBlock(ctx, reader, parent, block, result)
			}
			if e == nil {
				var snap contracts.StateReader
				snap, e = reader.Snapshot(ctx, head)
				if e == nil {
					if snap.Root() != result.StateRoot {
						e = fmt.Errorf("recovered result root mismatch")
					}
					snap.Close()
				}
			}
		}
	}
	return e
}
func (s *Store) Stop(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	s.stopped = true
	if s.starting {
		cancel, done := s.startCancel, s.startDone
		cancel()
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	e := s.db.Close()
	s.db = nil
	return e
}
func (s *Store) view(ctx context.Context, fn func(*db.Tx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return contracts.ErrClosed
	}
	return s.db.View(fn)
}
func (s *Store) Head(ctx context.Context) (contracts.ID, error) {
	var h contracts.ID
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "meta", []byte("head"), &h) })
	return h, e
}
func (s *Store) Block(ctx context.Context, id contracts.ID) (contracts.Block, error) {
	var data []byte
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "blocks", []byte(id), &data) })
	if e != nil {
		return nil, e
	}
	return ledger.DecodeBlock(s.p, data, id)
}
func (s *Store) Receipt(ctx context.Context, id contracts.ID) (contracts.Receipt, error) {
	var r contracts.Receipt
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "receipts", []byte(id), &r) })
	return r, e
}
func (s *Store) Transaction(ctx context.Context, id contracts.ID) (TransactionRecord, error) {
	var r ledger.Record
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "transactions", []byte(id), &r) })
	if e != nil {
		return TransactionRecord{}, e
	}
	t, e := ledger.DecodeTransaction(s.p, r.Data, id)
	return TransactionRecord{Transaction: t, BlockID: r.BlockID, Index: r.Index}, e
}
func (s *Store) Snapshot(ctx context.Context, id contracts.ID) (contracts.StateReader, error) {
	var state map[string][]byte
	var root contracts.Commitment
	e := s.view(ctx, func(tx *db.Tx) error {
		if e := get(tx, "states", []byte(id), &state); e != nil {
			return e
		}
		return get(tx, "roots", []byte(id), &root)
	})
	if e != nil {
		return nil, e
	}
	actual, e := s.p.StateCommitment(ctx, state)
	if e != nil {
		return nil, e
	}
	if root != actual {
		return nil, fmt.Errorf("corrupt state")
	}
	return &ledger.Snapshot{State: state, Commitment: root}, nil
}
func (s *Store) Commit(ctx context.Context, parent contracts.ID, b contracts.Block, r contracts.ExecutionResult) error {
	if b == nil {
		return fmt.Errorf("nil block")
	}
	var exists bool
	head, e := s.Head(ctx)
	if e != nil {
		return e
	}
	e = s.view(ctx, func(tx *db.Tx) error { exists = tx.Bucket([]byte("blocks")).Get([]byte(b.ID())) != nil; return nil })
	if e != nil {
		return e
	}
	if exists {
		return contracts.ErrAlreadyCommitted
	}
	if parent != head {
		return contracts.ErrConflict
	}
	v, e := ledger.Prepare(ctx, s.p, s, parent, b, r)
	if e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return contracts.ErrClosed
	}
	var callbackErr error
	e = s.db.Update(func(tx *db.Tx) error { callbackErr = s.commit(ctx, tx, parent, v); return callbackErr })
	if e != nil && callbackErr == nil {
		return fmt.Errorf("%w: %v", contracts.ErrCommitUnknown, e)
	}
	return e
}
func (s *Store) commit(ctx context.Context, tx *db.Tx, parent contracts.ID, v ledger.Prepared) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if tx.Bucket([]byte("blocks")).Get([]byte(v.ID)) != nil {
		return contracts.ErrAlreadyCommitted
	}
	var head contracts.ID
	if e := get(tx, "meta", []byte("head"), &head); e != nil {
		return e
	}
	if head != parent {
		return contracts.ErrConflict
	}
	for id := range v.Transactions {
		if tx.Bucket([]byte("transactions")).Get([]byte(id)) != nil {
			return fmt.Errorf("transaction already confirmed")
		}
	}
	for _, item := range []struct {
		b string
		v any
	}{{"blocks", v.Block}, {"states", v.State}, {"roots", v.Root}, {"results", v.Result}} {
		if e := put(tx, item.b, []byte(v.ID), item.v); e != nil {
			return e
		}
	}
	for id, r := range v.Transactions {
		if e := put(tx, "transactions", []byte(id), r); e != nil {
			return e
		}
	}
	for id, r := range v.Receipts {
		if e := put(tx, "receipts", []byte(id), r); e != nil {
			return e
		}
	}
	if s.beforeHead != nil {
		if e := s.beforeHead(); e != nil {
			return e
		}
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return put(tx, "meta", []byte("head"), v.ID)
}
