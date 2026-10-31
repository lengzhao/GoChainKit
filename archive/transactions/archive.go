// Package transactions retains canonical bodies with an independently owned backend.
package transactions

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	storagecap "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
	"sync"
)

type Config struct {
	MaxBytes            int64  `json:"maxBytes"`
	MaxTransactions     uint64 `json:"maxTransactions"`
	MaxTransactionBytes int    `json:"maxTransactionBytes"`
}
type Protocol interface {
	core.TransactionProtocol
	core.GenesisProvider
	core.BlockCodec
	core.StateCommitter
}
type Deps struct {
	Protocol Protocol           `json:"protocol"`
	Backend  storagecap.Backend `json:"backend"`
}
type Archive struct {
	mu          sync.RWMutex
	cfg         Config
	p           Protocol
	backend     storagecap.Backend
	db          storagecap.Database
	stopped     bool
	starting    bool
	startDone   chan struct{}
	startCancel context.CancelFunc
	stopDone    chan struct{}
	stopErr     error
}

func New(c Config, d Deps) (*Archive, error) {
	if d.Protocol == nil || d.Backend == nil {
		return nil, fmt.Errorf("archive: protocol and backend required")
	}
	if e := d.Protocol.Protocol().Validate(); e != nil {
		return nil, e
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 1 << 30
	}
	if c.MaxTransactions == 0 {
		c.MaxTransactions = 1000000
	}
	if c.MaxTransactionBytes == 0 {
		c.MaxTransactionBytes = 2 << 20
	}
	if c.MaxBytes < 1 || c.MaxTransactionBytes < 1 || c.MaxTransactionBytes > 64<<20 {
		return nil, fmt.Errorf("archive: invalid budget")
	}
	return &Archive{cfg: c, p: d.Protocol, backend: d.Backend}, nil
}
func (a *Archive) Protocol() core.ProtocolDescriptor { return a.p.Protocol() }
func (a *Archive) canonical(ctx context.Context, raw []byte, id core.ID) (core.Transaction, error) {
	if len(raw) > a.cfg.MaxTransactionBytes {
		return nil, core.ErrFull
	}
	t, e := a.p.DecodeTransaction(bytes.Clone(raw))
	if e != nil {
		return nil, e
	}
	if t == nil || t.ID() == "" || t.ID() != id {
		return nil, fmt.Errorf("archive: identity mismatch")
	}
	if e = a.p.ValidateTransaction(ctx, t); e != nil {
		return nil, e
	}
	b, e := a.p.EncodeTransaction(t)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(raw, b) {
		return nil, fmt.Errorf("archive: noncanonical transaction")
	}
	return t, nil
}
func (a *Archive) Start(ctx context.Context) (out error) {
	a.mu.Lock()
	if a.stopped || a.starting || a.db != nil {
		a.mu.Unlock()
		return core.ErrClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.starting = true
	a.startDone = done
	a.startCancel = cancel
	a.mu.Unlock()
	var db storagecap.Database
	defer func() {
		a.mu.Lock()
		if out == nil {
			out = ctx.Err()
			if a.stopped && out == nil {
				out = core.ErrClosed
			}
		}
		if out == nil {
			a.db = db
		}
		a.mu.Unlock()
		if out != nil && db != nil {
			out = errors.Join(out, db.Close())
		}
		a.mu.Lock()
		a.starting = false
		a.startCancel = nil
		close(done)
		a.mu.Unlock()
		cancel()
	}()
	var e error
	db, e = a.backend.Open(ctx)
	if e != nil {
		return e
	}
	genesis, state, e := a.p.Genesis(ctx)
	if e != nil {
		return e
	}
	if genesis == nil || genesis.ID() == "" {
		return fmt.Errorf("archive: invalid genesis")
	}
	encoded, e := a.p.EncodeBlock(genesis)
	if e != nil {
		return e
	}
	root, e := a.p.StateCommitment(ctx, state)
	if e != nil {
		return e
	}
	binding, e := json.Marshal(struct {
		Version                       int
		Protocol                      core.ProtocolDescriptor
		GenesisID, Genesis, StateRoot []byte
	}{1, a.Protocol(), []byte(genesis.ID()), encoded, []byte(root)})
	if e != nil {
		return e
	}
	e = db.Update(ctx, func(tx storagecap.Writer) error {
		ns, e := tx.Namespaces()
		if e != nil {
			return e
		}
		if len(ns) == 0 {
			if e = tx.CreateNamespace("meta"); e != nil {
				return e
			}
			if e = tx.CreateNamespace("bodies"); e != nil {
				return e
			}
			if e = tx.Put("meta", []byte("binding"), binding); e != nil {
				return e
			}
			return tx.Put("meta", []byte("usage"), make([]byte, 16))
		}
		b, e := tx.Get("meta", []byte("binding"))
		if e != nil {
			return e
		}
		if !bytes.Equal(b, binding) {
			return fmt.Errorf("archive: incompatible binding")
		}
		return nil
	})
	if e != nil {
		return e
	}
	// Read an owned snapshot under the backend transaction, then call codecs
	// outside it. Startup validation never holds a database lock across plugins.
	type record struct {
		id  core.ID
		raw []byte
	}
	var records []record
	e = db.View(ctx, func(tx storagecap.Reader) error {
		var count, total uint64
		if e := tx.Scan("bodies", nil, func(k, v []byte) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if len(v) > a.cfg.MaxTransactionBytes || total > uint64(a.cfg.MaxBytes) || uint64(len(v)) > uint64(a.cfg.MaxBytes)-total {
				return core.ErrFull
			}
			total += uint64(len(v))
			count++
			if count > a.cfg.MaxTransactions {
				return core.ErrFull
			}
			records = append(records, record{core.ID(k), bytes.Clone(v)})
			return nil
		}); e != nil {
			return e
		}
		usage, e := tx.Get("meta", []byte("usage"))
		if e != nil {
			return e
		}
		if len(usage) != 16 || binary.BigEndian.Uint64(usage) != count || binary.BigEndian.Uint64(usage[8:]) != total {
			return fmt.Errorf("archive: invalid usage")
		}
		return nil
	})
	if e != nil {
		return e
	}
	for _, r := range records {
		if _, e = a.canonical(ctx, r.raw, r.id); e != nil {
			return e
		}
	}
	return nil
}

func (a *Archive) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.stopDone == nil {
		a.stopDone = make(chan struct{})
		a.stopped = true
		if a.startCancel != nil {
			a.startCancel()
		}
		starting := a.starting
		startDone := a.startDone
		go func() {
			if starting {
				<-startDone
			}
			a.mu.Lock()
			db := a.db
			a.db = nil
			a.mu.Unlock()
			var e error
			if db != nil {
				e = db.Close()
			}
			a.mu.Lock()
			a.stopErr = e
			close(a.stopDone)
			a.mu.Unlock()
		}()
	}
	done := a.stopDone
	a.mu.Unlock()
	select {
	case <-done:
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *Archive) Put(ctx context.Context, t core.Transaction) error {
	if t == nil || t.ID() == "" {
		return core.ErrInvalidTransaction
	}
	id := t.ID()
	raw, e := a.p.EncodeTransaction(t)
	if e != nil {
		return e
	}
	if _, e = a.canonical(ctx, raw, id); e != nil {
		return e
	}
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return core.ErrClosed
	}
	return db.Update(ctx, func(tx storagecap.Writer) error {
		old, e := tx.Get("bodies", []byte(id))
		if e == nil {
			if !bytes.Equal(old, raw) {
				return fmt.Errorf("archive: conflicting body")
			}
			return nil
		}
		if e != storagecap.ErrNotFound {
			return e
		}
		usage, e := tx.Get("meta", []byte("usage"))
		if e != nil {
			return e
		}
		if len(usage) != 16 {
			return fmt.Errorf("archive: invalid usage")
		}
		count, total := binary.BigEndian.Uint64(usage), binary.BigEndian.Uint64(usage[8:])
		if total > uint64(a.cfg.MaxBytes) || count >= a.cfg.MaxTransactions || uint64(len(raw)) > uint64(a.cfg.MaxBytes)-total {
			return core.ErrFull
		}
		if e = tx.Put("bodies", []byte(id), raw); e != nil {
			return e
		}
		usage = binary.BigEndian.AppendUint64(nil, count+1)
		usage = binary.BigEndian.AppendUint64(usage, total+uint64(len(raw)))
		return tx.Put("meta", []byte("usage"), usage)
	})
}
func (a *Archive) Transaction(ctx context.Context, id core.ID) (out core.Transaction, err error) {
	defer func() {
		if err != nil && !pureMissing(err) && !canceled(err) {
			err = errors.Join(bodycap.ErrUnavailable, err)
		}
	}()
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return nil, core.ErrClosed
	}
	var raw []byte
	e := db.View(ctx, func(tx storagecap.Reader) error { var e error; raw, e = tx.Get("bodies", []byte(id)); return e })
	if e == storagecap.ErrNotFound {
		return nil, core.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	return a.canonical(ctx, raw, id)
}
func pureMissing(e error) bool {
	if e == core.ErrNotFound {
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return pureMissing(w.Unwrap())
	}
	return false
}
func canceled(e error) bool {
	if e == context.Canceled || e == context.DeadlineExceeded {
		return true
	}
	if many, ok := e.(interface{ Unwrap() []error }); ok {
		if len(many.Unwrap()) == 0 {
			return false
		}
		for _, child := range many.Unwrap() {
			if !canceled(child) {
				return false
			}
		}
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return canceled(w.Unwrap())
	}
	return false
}

var _ bodycap.TransactionArchive = (*Archive)(nil)
