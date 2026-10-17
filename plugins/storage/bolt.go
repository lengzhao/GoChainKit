// Package bolt persists protocol-neutral records using synchronous transactions.
package storage

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	db "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var buckets = []string{"meta", "blocks", "states", "transactions", "receipts", "roots", "results"}

type BoltConfig struct {
	Path string `json:"path"`
}
type TransactionRecord = core.ConfirmedTransaction
type BoltStore struct {
	mu          sync.RWMutex
	owner       *core.LedgerIdentity
	cfg         BoltConfig
	p           core.LedgerProtocol
	policy      chaincap.BranchPolicy
	db          *db.DB
	stopped     bool
	starting    bool
	startDone   chan struct{}
	startCancel context.CancelFunc
	beforeHead  func() error
}

func init() { pluginkit.Register("storage/bolt", NewBolt) }
func NewBolt(c BoltConfig, d Deps) (*BoltStore, error) {
	if c.Path == "" || d.Protocol == nil {
		return nil, fmt.Errorf("path and protocol required")
	}
	if e := d.Protocol.Protocol().Validate(); e != nil {
		return nil, e
	}
	policy, e := resolvePolicy(d)
	if e != nil {
		return nil, e
	}
	return &BoltStore{policy: policy, owner: core.NewLedgerIdentity(), cfg: c, p: d.Protocol}, nil
}
func (s *BoltStore) Protocol() core.ProtocolDescriptor { return s.p.Protocol() }
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
		return core.ErrNotFound
	}
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
func (s *BoltStore) Start(ctx context.Context) (err error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return core.ErrClosed
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
			err = core.ErrClosed
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
	g, e := prepareGenesis(ctx, s.p)
	if e != nil {
		return e
	}
	desc := s.p.Protocol()
	var genesisBranch branchNode
	wp, _ := s.p.(chaincap.BranchProtocol)
	isWork := s.policy != nil
	if isWork {
		genesisBranch, e = branchGenesis(ctx, wp, s.policy, g)
		if e != nil {
			return e
		}
	}
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
			var stored core.ProtocolDescriptor
			if e := get(tx, "meta", []byte("protocol"), &stored); e != nil {
				return e
			}
			storedPolicy := string(meta.Get([]byte("branchPolicy")))
			expectedPolicy := ""
			if s.policy != nil {
				expectedPolicy = s.policy.PolicyID()
			}
			if storedPolicy != expectedPolicy {
				return fmt.Errorf("database branch policy mismatch")
			}
			if stored != desc {
				return fmt.Errorf("database protocol mismatch")
			}
			var genesisID core.ID
			var genesisRoot core.Commitment
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
			if isWork != (tx.Bucket([]byte("branches")) != nil) {
				return fmt.Errorf("database branch capability mismatch")
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
		if isWork {
			if e := tx.Bucket([]byte("meta")).Put([]byte("branchPolicy"), []byte(s.policy.PolicyID())); e != nil {
				return e
			}
			if _, e := tx.CreateBucket([]byte("branches")); e != nil {
				return e
			}
			if e := put(tx, "branches", []byte(g.ID), genesisBranch); e != nil {
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
	reader := &BoltStore{owner: s.owner, p: s.p, policy: s.policy, db: database}
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
		var result core.ExecutionResult
		e = reader.view(ctx, func(tx *db.Tx) error { return get(tx, "results", []byte(head), &result) })
		if e == nil {
			var parent, block core.Block
			parent, e = reader.Block(ctx, result.Parent)
			if e == nil {
				block, e = reader.Block(ctx, head)
			}
			if e == nil {
				e = s.p.ValidateBlock(ctx, reader, parent, block, result)
			}
			if e == nil {
				var snap core.StateReader
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
	if e == nil && isWork {
		e = reader.validateBranches(ctx, wp, g)
	}
	return e
}
func (s *BoltStore) Stop(ctx context.Context) error {
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
func (s *BoltStore) view(ctx context.Context, fn func(*db.Tx) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return core.ErrClosed
	}
	return s.db.View(fn)
}
func (s *BoltStore) Head(ctx context.Context) (core.ID, error) {
	var h core.ID
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "meta", []byte("head"), &h) })
	return h, e
}
func (s *BoltStore) Block(ctx context.Context, id core.ID) (core.Block, error) {
	var data []byte
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "blocks", []byte(id), &data) })
	if e != nil {
		return nil, e
	}
	return decodeStoredBlock(s.p, data, id)
}
func (s *BoltStore) Receipt(ctx context.Context, id core.ID) (core.Receipt, error) {
	var r core.Receipt
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "receipts", []byte(id), &r) })
	return r, e
}
func (s *BoltStore) Transaction(ctx context.Context, id core.ID) (TransactionRecord, error) {
	var r storedTransaction
	e := s.view(ctx, func(tx *db.Tx) error { return get(tx, "transactions", []byte(id), &r) })
	if e != nil {
		return TransactionRecord{}, e
	}
	t, e := decodeStoredTransaction(s.p, r.Data, id)
	return TransactionRecord{Transaction: t, BlockID: r.BlockID, Index: r.Index}, e
}
func (s *BoltStore) Snapshot(ctx context.Context, id core.ID) (core.StateReader, error) {
	var state map[string][]byte
	var root core.Commitment
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
	return &stateSnapshot{State: state, Commitment: root}, nil
}
func (s *BoltStore) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
	if s.policy != nil {
		_, e := s.importBlock(ctx, parent, b, r, true)
		return e
	}
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
		return core.ErrAlreadyCommitted
	}
	if parent != head {
		return core.ErrConflict
	}
	v, e := prepareCommit(ctx, s.p, s, parent, b, r)
	if e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return core.ErrClosed
	}
	var callbackErr error
	e = s.db.Update(func(tx *db.Tx) error { callbackErr = s.commit(ctx, tx, parent, v); return callbackErr })
	if e != nil && callbackErr == nil {
		return fmt.Errorf("%w: %v", core.ErrCommitUnknown, e)
	}
	return e
}
func (s *BoltStore) commit(ctx context.Context, tx *db.Tx, parent core.ID, v preparedCommit) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if tx.Bucket([]byte("blocks")).Get([]byte(v.ID)) != nil {
		return core.ErrAlreadyCommitted
	}
	var head core.ID
	if e := get(tx, "meta", []byte("head"), &head); e != nil {
		return e
	}
	if head != parent {
		return core.ErrConflict
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

func (s *BoltStore) LedgerID() *core.LedgerIdentity { return s.owner }

var _ core.Ledger = (*BoltStore)(nil)
