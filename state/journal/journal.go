// Package journal implements bounded, execution-local state overlays. It does
// not persist state and is deliberately confined to a single execution goroutine.
package journal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"sync/atomic"

	capstate "github.com/lengzhao/GoChainKit/cap/state"
)

var (
	ErrLimit      = errors.New("journal resource limit exceeded")
	ErrCheckpoint = errors.New("invalid journal checkpoint")
	ErrConfig     = errors.New("invalid journal configuration")
)

// Config bounds retained overlay data, including all outstanding checkpoints.
// MaxBytes counts key and value bytes, MaxEntries counts keys (including deletes).
// Zero fields use defaults: 65,536 entries, 64 MiB, 64 checkpoints. Limits are
// local execution policy and must be identical when used in consensus execution.
type Config struct {
	MaxEntries         int `json:"maxEntries"`
	MaxBytes           int `json:"maxBytes"`
	MaxCheckpointDepth int `json:"maxCheckpointDepth"`
}
type Factory struct{ cfg Config }

func New(c Config) (*Factory, error) {
	if c.MaxEntries < 0 || c.MaxBytes < 0 || c.MaxCheckpointDepth < 0 {
		return nil, ErrConfig
	}
	if c.MaxEntries == 0 {
		c.MaxEntries = 65536
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 64 << 20
	}
	if c.MaxCheckpointDepth == 0 {
		c.MaxCheckpointDepth = 64
	}
	return &Factory{cfg: c}, nil
}

var instances atomic.Uint64

func (f *Factory) New(ctx context.Context, r capstate.Reader) (capstate.Journal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || isNil(r) {
		return nil, fmt.Errorf("journal: baseline required")
	}
	id := instances.Add(1)
	if id == 0 {
		return nil, ErrLimit
	}
	return &overlay{base: r, cfg: f.cfg, instance: id, values: make(map[string]value)}, nil
}
func isNil(v any) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return r.IsNil()
	}
	return false
}

type value struct {
	data    []byte
	deleted bool
}
type frame struct {
	token  capstate.Checkpoint
	values map[string]value
	size   int
}
type overlay struct {
	base               capstate.Reader
	cfg                Config
	instance, sequence uint64
	values             map[string]value
	size               int
	stack              []frame
}

func (o *overlay) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if v, ok := o.values[key]; ok {
		if v.deleted {
			return nil, false, nil
		}
		return bytes.Clone(v.data), true, nil
	}
	v, ok, err := o.base.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return bytes.Clone(v), true, nil
}
func (o *overlay) Set(ctx context.Context, key string, data []byte) error {
	return o.put(ctx, key, value{data: data})
}
func (o *overlay) Delete(ctx context.Context, key string) error {
	return o.put(ctx, key, value{deleted: true})
}
func (o *overlay) put(ctx context.Context, key string, v value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	count := len(o.values)
	size := o.size
	if old, ok := o.values[key]; ok {
		size -= len(key) + len(old.data)
	} else {
		count++
	}
	// Use subtraction before addition so large inputs cannot overflow the budget.
	heldEntries, heldBytes := o.retained()
	heldEntries -= len(o.values)
	heldBytes -= o.size
	available := o.cfg.MaxBytes - heldBytes - size
	if count > o.cfg.MaxEntries-heldEntries || len(key) > available || len(v.data) > available-len(key) {
		return ErrLimit
	}
	v.data = bytes.Clone(v.data)
	if err := ctx.Err(); err != nil {
		return err
	}
	o.values[key] = v
	o.size = size + len(key) + len(v.data)
	return nil
}
func (o *overlay) retained() (int, int) {
	n, b := len(o.values), o.size
	for _, f := range o.stack {
		n += len(f.values)
		b += f.size
	}
	return n, b
}
func clone(m map[string]value) map[string]value {
	c := make(map[string]value, len(m))
	for k, v := range m {
		v.data = bytes.Clone(v.data)
		c[k] = v
	}
	return c
}
func (o *overlay) Checkpoint(ctx context.Context) (capstate.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	n, b := o.retained()
	if len(o.stack) >= o.cfg.MaxCheckpointDepth || len(o.values) > o.cfg.MaxEntries-n || o.size > o.cfg.MaxBytes-b || o.sequence == math.MaxUint64 {
		return "", ErrLimit
	}
	values := clone(o.values)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.sequence++
	token := capstate.Checkpoint(fmt.Sprintf("%x:%x", o.instance, o.sequence))
	o.stack = append(o.stack, frame{token, values, o.size})
	return token, nil
}
func (o *overlay) pop(ctx context.Context, token capstate.Checkpoint, revert bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n := len(o.stack)
	if n == 0 || o.stack[n-1].token != token {
		return ErrCheckpoint
	}
	f := o.stack[n-1]
	if revert {
		o.values = f.values
		o.size = f.size
	}
	o.stack[n-1] = frame{}
	o.stack = o.stack[:n-1]
	return nil
}
func (o *overlay) Revert(ctx context.Context, t capstate.Checkpoint) error {
	return o.pop(ctx, t, true)
}
func (o *overlay) Release(ctx context.Context, t capstate.Checkpoint) error {
	return o.pop(ctx, t, false)
}
func (o *overlay) Changes(ctx context.Context) (capstate.Changes, error) {
	if err := ctx.Err(); err != nil {
		return capstate.Changes{}, err
	}
	c := capstate.Changes{Writes: make(map[string][]byte)}
	for k, v := range o.values {
		if err := ctx.Err(); err != nil {
			return capstate.Changes{}, err
		}
		if v.deleted {
			c.Deletes = append(c.Deletes, k)
		} else {
			c.Writes[k] = bytes.Clone(v.data)
		}
	}
	sort.Strings(c.Deletes)
	if err := ctx.Err(); err != nil {
		return capstate.Changes{}, err
	}
	return c, nil
}

var _ capstate.Factory = (*Factory)(nil)
var _ capstate.Journal = (*overlay)(nil)
