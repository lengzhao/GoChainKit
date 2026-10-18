// Package backend implements transactional storage without blockchain semantics.
package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"sort"
	"sync"
)

type rawTx interface {
	namespaces() ([]string, error)
	get(string, []byte) ([]byte, error)
	scan(string, []byte, func([]byte, []byte) error) error
	create(string) error
	put(string, []byte, []byte) error
	delete(string, []byte) error
}
type scope struct {
	mu     sync.Mutex
	active bool
	ctx    context.Context
	tx     rawTx
}
type reader struct{ s *scope }
type writer struct{ reader }

func (r reader) call(fn func(rawTx) error) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if !r.s.active {
		return capstorage.ErrExpired
	}
	if e := r.s.ctx.Err(); e != nil {
		return e
	}
	return fn(r.s.tx)
}
func (r reader) Namespaces() (v []string, e error) {
	e = r.call(func(t rawTx) error { var x error; v, x = t.namespaces(); return x })
	return
}
func (r reader) HasNamespace(n string) (v bool, e error) {
	e = r.call(func(t rawTx) error {
		ns, x := t.namespaces()
		if x != nil {
			return x
		}
		for _, s := range ns {
			if s == n {
				v = true
			}
		}
		return nil
	})
	return
}
func (r reader) Get(n string, k []byte) (v []byte, e error) {
	e = r.call(func(t rawTx) error { var x error; v, x = t.get(n, k); return x })
	return
}
func (r reader) Scan(n string, p []byte, fn func([]byte, []byte) error) error {
	if fn == nil {
		return fmt.Errorf("scan callback required")
	}
	type entry struct{ k, v []byte }
	var items []entry
	if e := r.call(func(t rawTx) error {
		return t.scan(n, p, func(k, v []byte) error { items = append(items, entry{k, v}); return nil })
	}); e != nil {
		return e
	}
	for _, item := range items {
		if e := r.call(func(rawTx) error { return nil }); e != nil {
			return e
		}
		if e := fn(item.k, item.v); e != nil {
			return e
		}
	}
	return nil
}

func (w writer) CreateNamespace(n string) error {
	if n == "" {
		return fmt.Errorf("empty namespace")
	}
	return w.call(func(t rawTx) error { return t.create(n) })
}
func (w writer) Put(n string, k, v []byte) error {
	if len(k) == 0 {
		return fmt.Errorf("empty key")
	}
	return w.call(func(t rawTx) error { return t.put(n, k, v) })
}
func (w writer) Delete(n string, k []byte) error {
	return w.call(func(t rawTx) error { return t.delete(n, k) })
}
func invoke(ctx context.Context, t rawTx, read func(capstorage.Reader) error, write func(capstorage.Writer) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s := &scope{active: true, ctx: ctx, tx: t}
	defer func() { s.mu.Lock(); s.active = false; s.tx = nil; s.mu.Unlock() }()
	var e error
	if write != nil {
		e = write(writer{reader{s}})
	} else if read != nil {
		e = read(reader{s})
	} else {
		return fmt.Errorf("transaction callback required")
	}
	if ctx.Err() == nil {
		return e
	}
	return errors.Join(e, ctx.Err())
}

type memoryFactory struct{}

func NewMemory() capstorage.Backend { return memoryFactory{} }
func (memoryFactory) Open(ctx context.Context) (capstorage.Database, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return &memoryDB{data: map[string]map[string][]byte{}}, nil
}

type memoryDB struct {
	mu     sync.RWMutex
	closed bool
	data   map[string]map[string][]byte
}

func (d *memoryDB) Close() error { d.mu.Lock(); defer d.mu.Unlock(); d.closed = true; return nil }
func (d *memoryDB) View(ctx context.Context, fn func(capstorage.Reader) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return capstorage.ErrClosed
	}
	return invoke(ctx, memoryTx(d.data), fn, nil)
}
func (d *memoryDB) Update(ctx context.Context, fn func(capstorage.Writer) error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return capstorage.ErrClosed
	}
	copy := map[string]map[string][]byte{}
	for n, b := range d.data {
		copy[n] = map[string][]byte{}
		for k, v := range b {
			copy[n][k] = bytes.Clone(v)
		}
	}
	if e := invoke(ctx, memoryTx(copy), nil, fn); e != nil {
		return e
	}
	d.data = copy
	return nil
}

type memoryTx map[string]map[string][]byte

func (t memoryTx) namespaces() ([]string, error) {
	v := make([]string, 0, len(t))
	for n := range t {
		v = append(v, n)
	}
	sort.Strings(v)
	return v, nil
}
func (t memoryTx) get(n string, k []byte) ([]byte, error) {
	b, ok := t[n]
	if !ok {
		return nil, capstorage.ErrNotFound
	}
	v, ok := b[string(k)]
	if !ok {
		return nil, capstorage.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (t memoryTx) scan(n string, p []byte, fn func([]byte, []byte) error) error {
	b, ok := t[n]
	if !ok {
		return capstorage.ErrNotFound
	}
	keys := make([]string, 0, len(b))
	for k := range b {
		if bytes.HasPrefix([]byte(k), p) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if e := fn([]byte(k), bytes.Clone(b[k])); e != nil {
			return e
		}
	}
	return nil
}
func (t memoryTx) create(n string) error {
	if _, ok := t[n]; ok {
		return fmt.Errorf("namespace exists")
	}
	t[n] = map[string][]byte{}
	return nil
}
func (t memoryTx) put(n string, k, v []byte) error {
	b, ok := t[n]
	if !ok {
		return capstorage.ErrNotFound
	}
	b[string(k)] = bytes.Clone(v)
	return nil
}
func (t memoryTx) delete(n string, k []byte) error {
	b, ok := t[n]
	if !ok {
		return capstorage.ErrNotFound
	}
	delete(b, string(k))
	return nil
}
