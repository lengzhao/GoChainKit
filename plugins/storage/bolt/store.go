// Package bolt persists a development chain using synchronous bbolt transactions.
// Local gob records are storage-format v1, not network or signing encodings.
package bolt

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/core/types"
	"github.com/lengzhao/pluginkit"
	db "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var buckets = []string{"meta", "blocks", "states", "transactions", "receipts"}

type Config struct {
	ChainID string `json:"chainID"`
	Path    string `json:"path"`
}
type TransactionRecord = types.ConfirmedTransaction
type Store struct {
	mu         sync.RWMutex
	cfg        Config
	db         *db.DB
	stopped    bool
	beforeHead func() error
}

func init() { pluginkit.Register("storage/bolt", New) }
func New(cfg Config) (*Store, error) {
	if cfg.Path == "" || cfg.ChainID == "" || len(cfg.ChainID) > types.MaxChainIDBytes {
		return nil, fmt.Errorf("storage/bolt: path and chainID required")
	}
	return &Store{cfg: cfg}, nil
}

// Method-free disk DTOs prevent gob from selecting Transaction.MarshalBinary,
// whose signing/network representation intentionally has no decoder yet.
type diskTransaction types.Transaction
type diskBlock struct {
	Header       types.BlockHeader
	Transactions []diskTransaction
}
type diskRecord struct {
	Transaction diskTransaction
	BlockID     types.Hash
	Height      uint64
	Index       uint64
}

func encode(v any) ([]byte, error) {
	switch value := v.(type) {
	case types.Block:
		transactions := make([]diskTransaction, len(value.Transactions))
		for i, transaction := range value.Transactions {
			transactions[i] = diskTransaction(transaction)
		}
		v = diskBlock{value.Header, transactions}
	case TransactionRecord:
		v = diskRecord{diskTransaction(value.Transaction), value.BlockID, value.Height, value.Index}
	}
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}
func put(tx *db.Tx, bucket string, key []byte, v any) error {
	data, err := encode(v)
	if err != nil {
		return err
	}
	return tx.Bucket([]byte(bucket)).Put(key, data)
}
func get(tx *db.Tx, bucket string, key []byte, v any) error {
	data := tx.Bucket([]byte(bucket)).Get(key)
	if data == nil {
		return contracts.ErrNotFound
	}
	decoder := gob.NewDecoder(bytes.NewReader(data))
	switch value := v.(type) {
	case *types.Block:
		var stored diskBlock
		if err := decoder.Decode(&stored); err != nil {
			return err
		}
		value.Header = stored.Header
		value.Transactions = make([]types.Transaction, len(stored.Transactions))
		for i, transaction := range stored.Transactions {
			value.Transactions[i] = types.Transaction(transaction)
		}
		return nil
	case *TransactionRecord:
		var stored diskRecord
		if err := decoder.Decode(&stored); err != nil {
			return err
		}
		*value = TransactionRecord{Transaction: types.Transaction(stored.Transaction), BlockID: stored.BlockID, Height: stored.Height, Index: stored.Index}
		return nil
	default:
		return decoder.Decode(v)
	}
}
func genesis(chain string) (types.Block, types.Hash, error) {
	root, _ := types.TransactionRoot(nil)
	b := types.Block{Header: types.BlockHeader{Version: 1, ChainID: chain, TransactionRoot: root, StateRoot: types.StateRoot(nil), ReceiptRoot: types.ReceiptRoot(nil)}}
	id, err := b.ID()
	return b, id, err
}
func (s *Store) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.stopped {
		return contracts.ErrClosed
	}
	if s.db != nil {
		return fmt.Errorf("storage already started")
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.Path), 0700); err != nil {
		return err
	}
	database, err := db.Open(s.cfg.Path, 0600, &db.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	// NoSync remains false: successful commit includes the backend's normal sync.
	err = database.Update(func(tx *db.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		meta := tx.Bucket([]byte("meta"))
		if meta != nil {
			if !bytes.Equal(meta.Get([]byte("format")), []byte("1")) {
				return fmt.Errorf("unsupported storage format")
			}
			if !bytes.Equal(meta.Get([]byte("chain")), []byte(s.cfg.ChainID)) {
				return fmt.Errorf("database chain mismatch")
			}
			for _, name := range buckets {
				if tx.Bucket([]byte(name)) == nil {
					return fmt.Errorf("missing bucket %s", name)
				}
			}
			var head types.Hash
			if err := get(tx, "meta", []byte("head"), &head); err != nil {
				return err
			}
			var b types.Block
			var state map[string][]byte
			if err := get(tx, "blocks", head[:], &b); err != nil {
				return err
			}
			if err := get(tx, "states", head[:], &state); err != nil {
				return err
			}
			id, err := b.ID()
			if err != nil {
				return err
			}
			if id != head || b.Header.ChainID != s.cfg.ChainID || types.StateRoot(state) != b.Header.StateRoot {
				return fmt.Errorf("inconsistent recovered head")
			}
			return nil
		}
		for _, name := range buckets {
			if tx.Bucket([]byte(name)) != nil {
				return fmt.Errorf("unrecognized database")
			}
			if _, err := tx.CreateBucket([]byte(name)); err != nil {
				return err
			}
		}
		meta = tx.Bucket([]byte("meta"))
		if err := meta.Put([]byte("format"), []byte("1")); err != nil {
			return err
		}
		if err := meta.Put([]byte("chain"), []byte(s.cfg.ChainID)); err != nil {
			return err
		}
		b, id, err := genesis(s.cfg.ChainID)
		if err != nil {
			return err
		}
		if err := put(tx, "blocks", id[:], b); err != nil {
			return err
		}
		if err := put(tx, "states", id[:], map[string][]byte{}); err != nil {
			return err
		}
		return put(tx, "meta", []byte("head"), id)
	})
	if err != nil {
		_ = database.Close()
		return err
	}
	s.db = database
	return nil
}
func (s *Store) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.db == nil {
		s.stopped = true
		return nil
	}
	err := s.db.Close()
	s.db = nil
	s.stopped = true
	return err
}
func (s *Store) view(ctx context.Context, fn func(*db.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return contracts.ErrClosed
	}
	return s.db.View(func(tx *db.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(tx)
	})
}
func (s *Store) Head(ctx context.Context) (types.Hash, error) {
	var h types.Hash
	err := s.view(ctx, func(tx *db.Tx) error { return get(tx, "meta", []byte("head"), &h) })
	return h, err
}
func (s *Store) Block(ctx context.Context, id types.Hash) (types.Block, error) {
	var b types.Block
	err := s.view(ctx, func(tx *db.Tx) error { return get(tx, "blocks", id[:], &b) })
	return b, err
}
func (s *Store) Receipt(ctx context.Context, id types.Hash) (types.Receipt, error) {
	var r types.Receipt
	err := s.view(ctx, func(tx *db.Tx) error { return get(tx, "receipts", id[:], &r) })
	return r, err
}
func (s *Store) Transaction(ctx context.Context, id types.Hash) (TransactionRecord, error) {
	var r TransactionRecord
	err := s.view(ctx, func(tx *db.Tx) error { return get(tx, "transactions", id[:], &r) })
	return r, err
}
func (s *Store) Snapshot(ctx context.Context, id types.Hash) (contracts.StateReader, error) {
	var state map[string][]byte
	err := s.view(ctx, func(tx *db.Tx) error { return get(tx, "states", id[:], &state) })
	if err != nil {
		return nil, err
	}
	return &snapshot{state}, nil
}
func (s *Store) Commit(ctx context.Context, parent types.Hash, b types.Block, result types.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b = b.Clone()
	id, err := b.ID()
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return contracts.ErrClosed
	}
	var callbackErr error
	err = s.db.Update(func(tx *db.Tx) error { callbackErr = s.commit(ctx, tx, parent, b, id, result); return callbackErr })
	if err != nil && callbackErr == nil {
		return fmt.Errorf("%w: %v", contracts.ErrCommitUnknown, err)
	}
	return err
}
func (s *Store) commit(ctx context.Context, tx *db.Tx, parent types.Hash, b types.Block, id types.Hash, r types.ExecutionResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.Bucket([]byte("blocks")).Get(id[:]) != nil {
		return contracts.ErrAlreadyCommitted
	}
	var head types.Hash
	if err := get(tx, "meta", []byte("head"), &head); err != nil {
		return err
	}
	if parent != head || b.Header.Parent != head {
		return contracts.ErrConflict
	}
	var previous types.Block
	if err := get(tx, "blocks", head[:], &previous); err != nil {
		return err
	}
	if previous.Header.Height == ^uint64(0) || b.Header.Height != previous.Header.Height+1 || b.Header.ChainID != s.cfg.ChainID {
		return fmt.Errorf("invalid ancestry")
	}
	if r.Parent != parent || r.BlockID != id || r.StateRoot != b.Header.StateRoot || types.ReceiptRoot(r.Receipts) != b.Header.ReceiptRoot || len(r.Receipts) != len(b.Transactions) {
		return fmt.Errorf("execution binding mismatch")
	}
	var state map[string][]byte
	if err := get(tx, "states", head[:], &state); err != nil {
		return err
	}
	if state == nil {
		state = make(map[string][]byte)
	}
	for k, v := range r.Writes {
		state[k] = bytes.Clone(v)
	}
	if types.StateRoot(state) != r.StateRoot {
		return fmt.Errorf("state root mismatch")
	}
	if err := put(tx, "blocks", id[:], b); err != nil {
		return err
	}
	if err := put(tx, "states", id[:], state); err != nil {
		return err
	}
	for i, transaction := range b.Transactions {
		if err := ctx.Err(); err != nil {
			return err
		}
		txid, err := transaction.ID()
		if err != nil {
			return err
		}
		if r.Receipts[i].TransactionID != txid {
			return fmt.Errorf("receipt mismatch")
		}
		if tx.Bucket([]byte("transactions")).Get(txid[:]) != nil {
			return fmt.Errorf("transaction already confirmed")
		}
		if err := put(tx, "transactions", txid[:], TransactionRecord{Transaction: transaction, BlockID: id, Height: b.Header.Height, Index: uint64(i)}); err != nil {
			return err
		}
		if err := put(tx, "receipts", txid[:], r.Receipts[i]); err != nil {
			return err
		}
	}
	if s.beforeHead != nil {
		if err := s.beforeHead(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return put(tx, "meta", []byte("head"), id)
}

type snapshot struct{ state map[string][]byte }

func (s *snapshot) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := s.state[string(key)]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s *snapshot) Entries(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(s.state))
	for k, v := range s.state {
		out[k] = bytes.Clone(v)
	}
	return out, nil
}
func (s *snapshot) Root() types.Hash { return types.StateRoot(s.state) }
func (s *snapshot) Close() error     { return nil }

var _ contracts.StateStore = (*Store)(nil)
var _ contracts.ChainStore = (*Store)(nil)
var _ contracts.Committer = (*Store)(nil)
var _ contracts.Lifecycle = (*Store)(nil)
var _ contracts.TransactionStore = (*Store)(nil)
