package journal_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	capstate "github.com/lengzhao/GoChainKit/cap/state"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/state/journal"
)

type baseline map[string][]byte

func (b baseline) Get(ctx context.Context, k string) ([]byte, bool, error) {
	v, ok := b[k]
	return v, ok, ctx.Err()
}
func factory(t *testing.T, c journal.Config) *journal.Factory {
	t.Helper()
	f, e := journal.New(c)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func create(t *testing.T, f *journal.Factory, b capstate.Reader) capstate.Journal {
	t.Helper()
	j, e := f.New(context.Background(), b)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestNestedReleaseAndRollback(t *testing.T) {
	ctx := context.Background()
	base := baseline{"a": []byte("original")}
	j := create(t, factory(t, journal.Config{}), base)
	outer, _ := j.Checkpoint(ctx)
	if e := j.Set(ctx, "a", []byte("outer")); e != nil {
		t.Fatal(e)
	}
	inner, _ := j.Checkpoint(ctx)
	_ = j.Delete(ctx, "a")
	_ = j.Set(ctx, "b", nil)
	if e := j.Release(ctx, inner); e != nil {
		t.Fatal(e)
	}
	if e := j.Revert(ctx, outer); e != nil {
		t.Fatal(e)
	}
	v, ok, e := j.Get(ctx, "a")
	if e != nil || !ok || string(v) != "original" {
		t.Fatal(v, ok, e)
	}
	c, _ := j.Changes(ctx)
	if len(c.Writes) != 0 || len(c.Deletes) != 0 {
		t.Fatal(c)
	}
	if _, ok, _ := j.Get(ctx, "b"); ok {
		t.Fatal("released inner survived outer revert")
	}
	if string(base["a"]) != "original" {
		t.Fatal("baseline mutated")
	}
}
func TestPresenceAndOwnership(t *testing.T) {
	ctx := context.Background()
	b := baseline{"nil": nil, "empty": {}, "a": []byte("value")}
	j := create(t, factory(t, journal.Config{}), b)
	for _, k := range []string{"nil", "empty"} {
		v, ok, e := j.Get(ctx, k)
		if e != nil || !ok || len(v) != 0 {
			t.Fatal(k, v, ok, e)
		}
	}
	v, _, _ := j.Get(ctx, "a")
	v[0] = 'x'
	if string(b["a"]) != "value" {
		t.Fatal("borrowed baseline bytes")
	}
	input := []byte("new")
	_ = j.Set(ctx, "a", input)
	input[0] = 'x'
	v, _, _ = j.Get(ctx, "a")
	v[0] = 'x'
	_ = j.Delete(ctx, "a")
	if _, ok, _ := j.Get(ctx, "a"); ok {
		t.Fatal("delete invisible")
	}
	_ = j.Set(ctx, "a", nil)
	c, _ := j.Changes(ctx)
	if _, ok := c.Writes["a"]; !ok || len(c.Deletes) != 0 {
		t.Fatal(c)
	}
	c.Writes["a"] = []byte("changed")
	v, ok, _ := j.Get(ctx, "a")
	if !ok || v != nil {
		t.Fatal(v, ok)
	}
	_ = j.Set(ctx, "bytes", []byte("safe"))
	c, _ = j.Changes(ctx)
	c.Writes["bytes"][0] = 'x'
	v, _, _ = j.Get(ctx, "bytes")
	if string(v) != "safe" {
		t.Fatal("changes alias")
	}
}
func TestInvalidTokensDoNotConsumeStack(t *testing.T) {
	ctx := context.Background()
	f := factory(t, journal.Config{})
	j := create(t, f, baseline{})
	other := create(t, f, baseline{})
	a, _ := j.Checkpoint(ctx)
	_ = j.Set(ctx, "a", []byte("a"))
	b, _ := j.Checkpoint(ctx)
	foreign, _ := other.Checkpoint(ctx)
	for _, tok := range []capstate.Checkpoint{a, foreign, "fabricated"} {
		if e := j.Revert(ctx, tok); !errors.Is(e, journal.ErrCheckpoint) {
			t.Fatal(e)
		}
	}
	if e := j.Release(ctx, b); e != nil {
		t.Fatal(e)
	}
	if e := j.Release(ctx, b); !errors.Is(e, journal.ErrCheckpoint) {
		t.Fatal(e)
	}
	if e := j.Revert(ctx, a); e != nil {
		t.Fatal(e)
	}
	c, _ := j.Changes(ctx)
	if len(c.Writes) != 0 {
		t.Fatal(c)
	}
}
func TestBudgetsAndCancellationAreAtomic(t *testing.T) {
	ctx := context.Background()
	j := create(t, factory(t, journal.Config{MaxEntries: 2, MaxBytes: 8, MaxCheckpointDepth: 1}), baseline{})
	_ = j.Set(ctx, "a", []byte("123"))
	tok, e := j.Checkpoint(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = j.Checkpoint(ctx); !errors.Is(e, journal.ErrLimit) {
		t.Fatal(e)
	}
	if e = j.Set(ctx, "b", nil); !errors.Is(e, journal.ErrLimit) {
		t.Fatal(e)
	}
	if e = j.Set(ctx, "a", []byte("1234")); !errors.Is(e, journal.ErrLimit) {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e = j.Delete(cancelled, "a"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = j.Release(cancelled, tok); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = j.Revert(ctx, tok); e != nil {
		t.Fatal(e)
	}
	v, _, _ := j.Get(ctx, "a")
	if string(v) != "123" {
		t.Fatal(string(v))
	}
	if _, e = j.Changes(cancelled); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type byteReader struct {
	value []byte
	err   error
	calls int
}

func (b *byteReader) Get(context.Context, []byte) ([]byte, error) { b.calls++; return b.value, b.err }
func TestAdapterPreservesBaselineFaults(t *testing.T) {
	ctx := context.Background()
	fault := errors.New("disk failure")
	for _, e := range []error{core.ErrNotFound, fmt.Errorf("wrapped: %w", core.ErrNotFound), errors.Join(core.ErrNotFound, core.ErrNotFound)} {
		b := &byteReader{err: e}
		r, err := journal.FromStateReader(b)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok, err := r.Get(ctx, "key"); err != nil || ok {
			t.Fatal(ok, err)
		}
	}
	for _, e := range []error{fault, errors.Join(core.ErrNotFound, fault)} {
		r, _ := journal.FromStateReader(&byteReader{err: e})
		if _, _, err := r.Get(ctx, "key"); !errors.Is(err, fault) {
			t.Fatal(err)
		}
	}
	b := &byteReader{value: []byte("owned")}
	r, _ := journal.FromStateReader(b)
	if b.calls != 0 {
		t.Fatal("adapter reads at construction")
	}
	j := create(t, factory(t, journal.Config{}), r)
	if b.calls != 0 {
		t.Fatal("factory reads baseline")
	}
	v, _, _ := j.Get(ctx, "a")
	v[0] = 'x'
	if string(b.value) != "owned" {
		t.Fatal("alias")
	}
}

func TestCheckpointOwnsHistoryAndReclaimsBudget(t *testing.T) {
	ctx := context.Background()
	j := create(t, factory(t, journal.Config{MaxEntries: 4, MaxBytes: 8}), baseline{})
	_ = j.Set(ctx, "a", []byte("123"))
	tok, _ := j.Checkpoint(ctx)
	_ = j.Set(ctx, "a", []byte("xyz"))
	if e := j.Release(ctx, tok); e != nil {
		t.Fatal(e)
	}
	tok, e := j.Checkpoint(ctx)
	if e != nil {
		t.Fatal("released checkpoint retained budget", e)
	}
	_ = j.Delete(ctx, "a")
	if e = j.Revert(ctx, tok); e != nil {
		t.Fatal(e)
	}
	v, ok, e := j.Get(ctx, "a")
	if e != nil || !ok || string(v) != "xyz" {
		t.Fatal(string(v), ok, e)
	}
	_ = j.Delete(ctx, "z")
	_ = j.Delete(ctx, "b")
	c, e := j.Changes(ctx)
	if e != nil || len(c.Deletes) != 2 || c.Deletes[0] != "b" || c.Deletes[1] != "z" {
		t.Fatal(c, e)
	}
}
func TestFactoryValidationAndCancellation(t *testing.T) {
	for _, c := range []journal.Config{{MaxEntries: -1}, {MaxBytes: -1}, {MaxCheckpointDepth: -1}} {
		if _, e := journal.New(c); !errors.Is(e, journal.ErrConfig) {
			t.Fatal(e)
		}
	}
	f := factory(t, journal.Config{})
	if _, e := f.New(context.Background(), nil); e == nil {
		t.Fatal("nil baseline")
	}
	var typed *byteReader
	if _, e := journal.FromStateReader(typed); e == nil {
		t.Fatal("typed nil adapter accepted")
	}
	if _, e := journal.FromStateReader(nil); e == nil {
		t.Fatal("nil adapter accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := f.New(cancelled, baseline{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	j := create(t, f, baseline{})
	if _, _, e := j.Get(cancelled, "a"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := j.Set(cancelled, "a", nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := j.Checkpoint(cancelled); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
