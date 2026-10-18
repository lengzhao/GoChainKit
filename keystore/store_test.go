package keystore

import (
	"bytes"
	"context"
	"errors"
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"os"
	"path/filepath"
	"testing"
)

type secret []byte

func (s secret) Secret(context.Context) ([]byte, error) { return bytes.Clone(s), nil }
func TestEncryptedRoundtrip(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "keys")
	s, e := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Create(ctx, "p2p", []byte("private material")); e != nil {
		t.Fatal(e)
	}
	v, e := s.Get(ctx, "p2p")
	if e != nil || string(v) != "private material" {
		t.Fatal(v, e)
	}
	v[0] = 0
	v, _ = s.Get(ctx, "p2p")
	if string(v) != "private material" {
		t.Fatal("borrowed bytes")
	}
	if e = s.Create(ctx, "p2p", []byte("replace")); !errors.Is(e, cap.ErrExists) {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "703270.key"))
	if e != nil || bytes.Contains(raw, []byte("private material")) {
		t.Fatal(e)
	}
	wrong, _ := NewEncryptedFile(FileConfig{Directory: dir}, secret("wrong"))
	if _, e = wrong.Get(ctx, "p2p"); !errors.Is(e, cap.ErrDecrypt) {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, "p2p"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, "p2p"); !errors.Is(e, cap.ErrNotFound) {
		t.Fatal(e)
	}
}
func TestMemoryOwned(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	v := []byte("abc")
	if e := s.Create(ctx, "id", v); e != nil {
		t.Fatal(e)
	}
	v[0] = 0
	got, _ := s.Get(ctx, "id")
	if string(got) != "abc" {
		t.Fatal(got)
	}
}

func TestFileBoundsTamperAndBinding(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "keys")
	s, _ := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	if e := s.Create(ctx, "one", []byte("key")); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(dir, "6f6e65.key")
	raw, _ := os.ReadFile(name)
	info, _ := os.Stat(name)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if e := os.WriteFile(filepath.Join(dir, "74776f.key"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get(ctx, "two"); !errors.Is(e, cap.ErrDecrypt) {
		t.Fatal("AAD", e)
	}
	raw[len(raw)-1] ^= 1
	os.WriteFile(name, raw, 0600)
	if _, e := s.Get(ctx, "one"); !errors.Is(e, cap.ErrDecrypt) {
		t.Fatal("tamper", e)
	}
	after, _ := os.ReadFile(name)
	if !bytes.Equal(after, raw) {
		t.Fatal("changed damaged record")
	}
	if e := s.Create(ctx, "", []byte("x")); !errors.Is(e, cap.ErrInvalid) {
		t.Fatal(e)
	}
	os.Symlink(name, filepath.Join(dir, "6c696e6b.key"))
	if _, e := s.Get(ctx, "link"); !errors.Is(e, cap.ErrInvalid) {
		t.Fatal("symlink", e)
	}
	os.WriteFile(filepath.Join(dir, "626967.key"), make([]byte, MaxMaterialBytes+100), 0600)
	if _, e := s.Get(ctx, "big"); !errors.Is(e, cap.ErrInvalid) {
		t.Fatal("size", e)
	}
}
func TestInterruptedTempAndConcurrentPublication(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	os.WriteFile(filepath.Join(dir, ".pending-abandoned"), []byte("partial"), 0600)
	s, _ := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	other, _ := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	done := make(chan error, 2)
	go func() { done <- s.Create(ctx, "p2p", []byte("first")) }()
	go func() { done <- other.Create(ctx, "p2p", []byte("second")) }()
	a, b := <-done, <-done
	if !((a == nil && errors.Is(b, cap.ErrExists)) || (b == nil && errors.Is(a, cap.ErrExists))) {
		t.Fatal(a, b)
	}
	v, e := s.Get(ctx, "p2p")
	if e != nil || (string(v) != "first" && string(v) != "second") {
		t.Fatal(v, e)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".pending-*"))
	if len(files) != 1 {
		t.Fatal(files)
	}
}

type failingSecret struct{ err error }

func (s failingSecret) Secret(context.Context) ([]byte, error) { return []byte("top-secret"), s.err }
func TestCancelledAndSourceSanitized(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, _ := NewEncryptedFile(FileConfig{Directory: filepath.Join(t.TempDir(), "keys")}, secret("pass"))
	if e := s.Create(ctx, "p2p", []byte("x")); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	cause := errors.New("top-secret")
	s, _ = NewEncryptedFile(FileConfig{Directory: filepath.Join(t.TempDir(), "keys")}, failingSecret{cause})
	e := s.Create(context.Background(), "p2p", []byte("x"))
	if !errors.Is(e, cause) || bytes.Contains([]byte(e.Error()), []byte("top-secret")) {
		t.Fatal(e)
	}
}

func TestConstructorsDoNotTouchFilesystemAndWaitCancels(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	s, e := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("constructor touched filesystem", e)
	}
	s.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.Get(ctx, "p2p"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	<-s.slots
	if _, e = s.Get(context.Background(), "p2p"); !errors.Is(e, cap.ErrNotFound) {
		t.Fatal(e)
	}
}
func TestExistingWideDirectoryAndSymlinkRootRejected(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	s, _ := NewEncryptedFile(FileConfig{Directory: dir}, secret("pass"))
	if e := s.Create(context.Background(), "id", []byte("x")); !errors.Is(e, cap.ErrInvalid) {
		t.Fatal(e)
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dir, link)
	s, _ = NewEncryptedFile(FileConfig{Directory: link}, secret("pass"))
	if _, e := s.Get(context.Background(), "id"); !errors.Is(e, cap.ErrInvalid) {
		t.Fatal(e)
	}
}
