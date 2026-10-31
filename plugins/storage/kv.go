// KVStore persists protocol-neutral records using a transactional backend.
package storage

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	chaincap "github.com/lengzhao/GoChainKit/cap/chain"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"sync"
)

var buckets = []string{"meta", "blocks", "states", "transactions", "receipts", "roots", "results"}

type KVConfig struct{}
type KVDeps struct {
	Archive      bodycap.TransactionArchive `json:"archive,omitempty"`
	Materializer bodycap.Materializer       `json:"materializer,omitempty"`
	Protocol     core.LedgerProtocol        `json:"protocol"`
	Policy       chaincap.BranchPolicy      `json:"policy,omitempty"`
	Backend      capstorage.Backend         `json:"backend"`
}
type TransactionRecord = core.ConfirmedTransaction
type KVStore struct {
	body        *bodyAccess
	mu          sync.RWMutex
	owner       *core.LedgerIdentity
	backend     capstorage.Backend
	p           core.LedgerProtocol
	policy      chaincap.BranchPolicy
	db          capstorage.Database
	stopped     bool
	starting    bool
	startDone   chan struct{}
	startCancel context.CancelFunc
	beforeHead  func() error
}

func init() { pluginkit.Register("storage/kv", NewKV) }
func NewKV(_ KVConfig, d KVDeps) (*KVStore, error) {
	if d.Backend == nil || d.Protocol == nil {
		return nil, fmt.Errorf("backend and protocol required")
	}
	if e := d.Protocol.Protocol().Validate(); e != nil {
		return nil, e
	}
	body, e := newBodyAccess(d.Protocol, d.Archive, d.Materializer)
	if e != nil {
		return nil, e
	}
	policy, e := resolvePolicy(Deps{Protocol: d.Protocol, Policy: d.Policy})
	if e != nil {
		return nil, e
	}
	return &KVStore{policy: policy, body: body, owner: core.NewLedgerIdentity(), backend: d.Backend, p: d.Protocol}, nil
}
func (s *KVStore) Protocol() core.ProtocolDescriptor { return s.p.Protocol() }
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), e
}
func put(tx capstorage.Writer, b string, k []byte, v any) error {
	data, e := encode(v)
	if e != nil {
		return e
	}
	return tx.Put(b, k, data)
}
func get(tx capstorage.Reader, b string, k []byte, v any) error {
	data, e := tx.Get(b, k)
	if e == capstorage.ErrNotFound {
		return core.ErrNotFound
	}
	if e != nil {
		return e
	}
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
func (s *KVStore) Start(ctx context.Context) (err error) {
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
	var database capstorage.Database
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
	g, e := prepareGenesis(ctx, s.p, s.body)
	if e != nil {
		return e
	}
	desc := s.p.Protocol()
	var genesisBranch branchNode
	wp, _ := s.p.(chaincap.BranchProtocol)
	isWork := s.policy != nil
	if isWork {
		genesisBranch, e = branchGenesis(ctx, wp, s.policy, g, s.body)
		if e != nil {
			return e
		}
	}
	database, e = s.backend.Open(ctx)
	if e != nil {
		return e
	}
	e = database.Update(ctx, func(tx capstorage.Writer) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if exists, e := tx.HasNamespace("meta"); e != nil {
			return e
		} else if exists {
			format, e := tx.Get("meta", []byte("format"))
			if e != nil {
				return e
			}
			if !bytes.Equal(format, []byte("2")) {
				return fmt.Errorf("unsupported storage format")
			}
			var stored core.ProtocolDescriptor
			if e := get(tx, "meta", []byte("protocol"), &stored); e != nil {
				return e
			}
			policyBytes, e := tx.Get("meta", []byte("branchPolicy"))
			if e != nil && e != capstorage.ErrNotFound {
				return e
			}
			storedPolicy := string(policyBytes)
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
			if has, e := tx.HasNamespace("branches"); e != nil {
				return e
			} else if isWork != has {
				return fmt.Errorf("database branch capability mismatch")
			}
			for _, b := range buckets {
				if has, e := tx.HasNamespace(b); e != nil {
					return e
				} else if !has {
					return fmt.Errorf("missing bucket")
				}
			}
			return nil
		}
		if names, e := tx.Namespaces(); e != nil {
			return e
		} else if len(names) != 0 {
			return fmt.Errorf("unrecognized database")
		}
		for _, b := range buckets {
			if e := tx.CreateNamespace(b); e != nil {
				return e
			}
		}
		if isWork {
			if e := tx.Put("meta", []byte("branchPolicy"), []byte(s.policy.PolicyID())); e != nil {
				return e
			}
			if e := tx.CreateNamespace("branches"); e != nil {
				return e
			}
			if e := put(tx, "branches", []byte(g.ID), genesisBranch); e != nil {
				return e
			}
		}
		if e := tx.Put("meta", []byte("format"), []byte("2")); e != nil {
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
	reader := &KVStore{body: s.body, owner: s.owner, p: s.p, policy: s.policy, db: database}
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
		e = reader.view(ctx, func(tx capstorage.Reader) error { return get(tx, "results", []byte(head), &result) })
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
func (s *KVStore) Stop(ctx context.Context) error {
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
func (s *KVStore) view(ctx context.Context, fn func(capstorage.Reader) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return core.ErrClosed
	}
	return s.db.View(ctx, fn)
}
func (s *KVStore) Head(ctx context.Context) (core.ID, error) {
	var h core.ID
	e := s.view(ctx, func(tx capstorage.Reader) error { return get(tx, "meta", []byte("head"), &h) })
	return h, e
}
func (s *KVStore) Block(ctx context.Context, id core.ID) (core.Block, error) {
	var data []byte
	e := s.view(ctx, func(tx capstorage.Reader) error { return get(tx, "blocks", []byte(id), &data) })
	if e != nil {
		return nil, e
	}
	return decodeAvailableBlock(ctx, s.p, data, id, s.body)
}
func (s *KVStore) Receipt(ctx context.Context, id core.ID) (core.Receipt, error) {
	var r core.Receipt
	e := s.view(ctx, func(tx capstorage.Reader) error { return get(tx, "receipts", []byte(id), &r) })
	return r, e
}
func (s *KVStore) Transaction(ctx context.Context, id core.ID) (TransactionRecord, error) {
	var r storedTransaction
	e := s.view(ctx, func(tx capstorage.Reader) error { return get(tx, "transactions", []byte(id), &r) })
	if e != nil {
		return TransactionRecord{}, e
	}
	t, e := decodeStoredTransaction(s.p, r.Data, id)
	return TransactionRecord{Transaction: t, BlockID: r.BlockID, Index: r.Index}, e
}
func (s *KVStore) Snapshot(ctx context.Context, id core.ID) (core.StateReader, error) {
	var state map[string][]byte
	var root core.Commitment
	e := s.view(ctx, func(tx capstorage.Reader) error {
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
func (s *KVStore) Commit(ctx context.Context, parent core.ID, b core.Block, r core.ExecutionResult) error {
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
	e = s.view(ctx, func(tx capstorage.Reader) error {
		var e error
		exists, e = hasRecord(tx, "blocks", []byte(b.ID()))
		return e
	})
	if e != nil {
		return e
	}
	if exists {
		return core.ErrAlreadyCommitted
	}
	if parent != head {
		return core.ErrConflict
	}
	v, e := prepareCommit(ctx, s.p, s, parent, b, r, s.body)
	if e != nil {
		return e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return core.ErrClosed
	}
	e = s.db.Update(ctx, func(tx capstorage.Writer) error { return s.commit(ctx, tx, parent, v) })
	if errors.Is(e, capstorage.ErrCommitUnknown) {
		return errors.Join(core.ErrCommitUnknown, e)
	}
	return e
}
func (s *KVStore) commit(ctx context.Context, tx capstorage.Writer, parent core.ID, v preparedCommit) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if exists, e := hasRecord(tx, "blocks", []byte(v.ID)); e != nil {
		return e
	} else if exists {
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
		if exists, e := hasRecord(tx, "transactions", []byte(id)); e != nil {
			return e
		} else if exists {
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

func (s *KVStore) LedgerID() *core.LedgerIdentity { return s.owner }

var _ core.Ledger = (*KVStore)(nil)

func hasRecord(tx capstorage.Reader, n string, k []byte) (bool, error) {
	_, e := tx.Get(n, k)
	if e == capstorage.ErrNotFound {
		return false, nil
	}
	return e == nil, e
}
