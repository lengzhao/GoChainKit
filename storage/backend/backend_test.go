package backend

import (
	"context"
	"errors"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestBackendContract(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "bolt"}[disk], func(t *testing.T) {
			ctx := context.Background()
			var f capstorage.Backend
			if disk {
				f, _ = NewBolt(BoltConfig{Path: filepath.Join(t.TempDir(), "data.db")})
			} else {
				f = NewMemory()
			}
			db, err := f.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var expired capstorage.Writer
			if err = db.Update(ctx, func(w capstorage.Writer) error {
				expired = w
				if e := w.CreateNamespace("n"); e != nil {
					return e
				}
				return w.Put("n", []byte("b"), []byte("two"))
			}); err != nil {
				t.Fatal(err)
			}
			if err = expired.Put("n", []byte("z"), nil); !errors.Is(err, capstorage.ErrExpired) {
				t.Fatalf("expired %v", err)
			}
			sentinel := errors.New("rollback")
			if err = db.Update(ctx, func(w capstorage.Writer) error { w.Put("n", []byte("a"), []byte("one")); return sentinel }); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if err = db.View(ctx, func(r capstorage.Reader) error {
				if _, ok := r.(capstorage.Writer); ok {
					t.Fatal("readonly exposes writer")
				}
				if _, e := r.Get("n", []byte("a")); !errors.Is(e, capstorage.ErrNotFound) {
					t.Fatal(e)
				}
				b, e := r.Get("n", []byte("b"))
				if e != nil {
					return e
				}
				b[0] = 'X'
				b, e = r.Get("n", []byte("b"))
				if string(b) != "two" {
					t.Fatal("borrowed bytes")
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
			cancelctx, cancel := context.WithCancel(ctx)
			if err = db.Update(cancelctx, func(w capstorage.Writer) error { w.Put("n", []byte("a"), []byte("one")); cancel(); return nil }); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err = db.Update(ctx, func(w capstorage.Writer) error { return w.Put("n", []byte("a"), []byte("one")) }); err != nil {
				t.Fatal(err)
			}
			if err = db.View(ctx, func(r capstorage.Reader) error {
				var keys string
				return r.Scan("n", nil, func(k, v []byte) error {
					keys += string(k)
					if len(keys) == 2 && keys != "ab" {
						t.Fatal(keys)
					}
					k[0] = 'X'
					v[0] = 'X'
					return nil
				})
			}); err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("no panic")
					}
				}()
				db.Update(ctx, func(w capstorage.Writer) error {
					expired = w
					w.Put("n", []byte("p"), []byte("bad"))
					panic("rollback")
				})
			}()
			if _, err = expired.Get("n", []byte("b")); !errors.Is(err, capstorage.ErrExpired) {
				t.Fatal(err)
			}
			if err = db.View(ctx, func(r capstorage.Reader) error {
				_, e := r.Get("n", []byte("p"))
				if !errors.Is(e, capstorage.ErrNotFound) {
					t.Fatal(e)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if err = db.View(ctx, func(capstorage.Reader) error { return nil }); !errors.Is(err, capstorage.ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}

func TestMemoryFactoryDoesNotShareDatabase(t *testing.T) {
	ctx := context.Background()
	f := NewMemory()
	a, _ := f.Open(ctx)
	defer a.Close()
	b, _ := f.Open(ctx)
	defer b.Close()
	a.Update(ctx, func(w capstorage.Writer) error { return w.CreateNamespace("a") })
	b.View(ctx, func(r capstorage.Reader) error {
		names, e := r.Namespaces()
		if len(names) != 0 || e != nil {
			t.Fatal(names, e)
		}
		return nil
	})
}

func TestBackendSnapshotAndClose(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "bolt"}[disk], func(t *testing.T) {
			ctx := context.Background()
			var f capstorage.Backend = NewMemory()
			if disk {
				f, _ = NewBolt(BoltConfig{Path: filepath.Join(t.TempDir(), "d.db")})
			}
			d, _ := f.Open(ctx)
			if e := d.Update(ctx, func(w capstorage.Writer) error { w.CreateNamespace("n"); return w.Put("n", []byte("k"), []byte("v")) }); e != nil {
				t.Fatal(e)
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- d.View(ctx, func(r capstorage.Reader) error {
					v, e := r.Get("n", []byte("k"))
					if e != nil {
						return e
					}
					close(entered)
					<-release
					again, e := r.Get("n", []byte("k"))
					if string(v) != string(again) {
						t.Error("inconsistent view")
					}
					return e
				})
			}()
			<-entered
			closed := make(chan error, 1)
			go func() { closed <- d.Close() }()
			select {
			case e := <-closed:
				t.Fatalf("closed before callback returned %v", e)
			default:
			}
			close(release)
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if e := <-closed; e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestBoltConstructorDoesNotCreateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "data.db")
	if _, e := NewBolt(BoltConfig{Path: path}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatalf("constructor performed IO: %v", e)
	}
}
