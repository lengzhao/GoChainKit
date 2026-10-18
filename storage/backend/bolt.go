package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	bolt "go.etcd.io/bbolt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type BoltConfig struct {
	Path string `json:"path"`
}
type boltFactory struct{ cfg BoltConfig }

// NewBolt validates configuration only. Open creates directories and acquires the file.
func NewBolt(c BoltConfig) (capstorage.Backend, error) {
	if c.Path == "" {
		return nil, fmt.Errorf("database path required")
	}
	return boltFactory{c}, nil
}
func (f boltFactory) Open(ctx context.Context) (capstorage.Database, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(filepath.Dir(f.cfg.Path), 0700); e != nil {
		return nil, e
	}
	d, e := bolt.Open(f.cfg.Path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		d.Close()
		return nil, e
	}
	return &boltDB{db: d}, nil
}

type boltDB struct {
	mu sync.RWMutex
	db *bolt.DB
}

func (d *boltDB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	e := d.db.Close()
	d.db = nil
	return e
}
func (d *boltDB) View(ctx context.Context, fn func(capstorage.Reader) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.db == nil {
		return capstorage.ErrClosed
	}
	return d.db.View(func(t *bolt.Tx) error { return invoke(ctx, boltTx{t}, fn, nil) })
}
func (d *boltDB) Update(ctx context.Context, fn func(capstorage.Writer) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.db == nil {
		return capstorage.ErrClosed
	}
	var callbackErr error
	invoked := false
	e := d.db.Update(func(t *bolt.Tx) error {
		invoked = true
		callbackErr = invoke(ctx, boltTx{t}, nil, fn)
		return callbackErr
	})
	if e != nil && invoked && callbackErr == nil {
		return errors.Join(capstorage.ErrCommitUnknown, e)
	}
	return e
}

type boltTx struct{ t *bolt.Tx }

func (t boltTx) namespaces() (v []string, e error) {
	e = t.t.ForEach(func(n []byte, _ *bolt.Bucket) error { v = append(v, string(n)); return nil })
	return
}
func (t boltTx) get(n string, k []byte) ([]byte, error) {
	b := t.t.Bucket([]byte(n))
	if b == nil {
		return nil, capstorage.ErrNotFound
	}
	v := b.Get(k)
	if v == nil {
		return nil, capstorage.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (t boltTx) scan(n string, p []byte, fn func([]byte, []byte) error) error {
	b := t.t.Bucket([]byte(n))
	if b == nil {
		return capstorage.ErrNotFound
	}
	c := b.Cursor()
	for k, v := c.Seek(p); k != nil && bytes.HasPrefix(k, p); k, v = c.Next() {
		if e := fn(bytes.Clone(k), bytes.Clone(v)); e != nil {
			return e
		}
	}
	return nil
}
func (t boltTx) create(n string) error { _, e := t.t.CreateBucket([]byte(n)); return e }
func (t boltTx) put(n string, k, v []byte) error {
	b := t.t.Bucket([]byte(n))
	if b == nil {
		return capstorage.ErrNotFound
	}
	return b.Put(bytes.Clone(k), bytes.Clone(v))
}
func (t boltTx) delete(n string, k []byte) error {
	b := t.t.Bucket([]byte(n))
	if b == nil {
		return capstorage.ErrNotFound
	}
	return b.Delete(k)
}
