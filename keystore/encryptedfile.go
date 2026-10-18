package keystore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"golang.org/x/crypto/scrypt"
	"io"
	"os"
	"path/filepath"
)

type FileConfig struct {
	Directory string `json:"directory"`
}
type EncryptedFile struct {
	dir    string
	source cap.SecretSource
	slots  chan struct{}
}
type secretError struct{ cause error }

func (e secretError) Error() string { return "keystore: secret source unavailable" }
func (e secretError) Unwrap() error { return e.cause }
func NewEncryptedFile(c FileConfig, s cap.SecretSource) (*EncryptedFile, error) {
	if c.Directory == "" || s == nil {
		return nil, fmt.Errorf("keystore: directory and secret source required")
	}
	return &EncryptedFile{dir: filepath.Clean(c.Directory), source: s, slots: make(chan struct{}, 1)}, nil
}
func (s *EncryptedFile) root(create bool) (*os.Root, error) {
	if create {
		if e := os.MkdirAll(s.dir, 0700); e != nil {
			return nil, e
		}
	}
	info, e := os.Lstat(s.dir)
	if errors.Is(e, os.ErrNotExist) {
		return nil, cap.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, cap.ErrInvalid
	}
	return os.OpenRoot(s.dir)
}
func filename(id cap.ID) string { return hex.EncodeToString([]byte(id)) + ".key" }
func (s *EncryptedFile) aead(ctx context.Context, salt []byte) (cipher.AEAD, error) {
	v, e := s.source.Secret(ctx)
	if e != nil {
		clear(v)
		return nil, secretError{e}
	}
	defer clear(v)
	if len(v) == 0 {
		return nil, secretError{cap.ErrInvalid}
	}
	key, e := scrypt.Key(v, salt, 1<<17, 8, 1, 32)
	if e != nil {
		return nil, e
	}
	defer clear(key)
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (s *EncryptedFile) slot(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func aad(id cap.ID) []byte { return append([]byte("GoChainKit/key/1/"), []byte(id)...) }
func (s *EncryptedFile) Get(ctx context.Context, id cap.ID) ([]byte, error) {
	if !valid(id) {
		return nil, cap.ErrInvalid
	}
	if e := s.slot(ctx); e != nil {
		return nil, e
	}
	defer func() { <-s.slots }()
	r, e := s.root(false)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	name := filename(id)
	info, e := r.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		return nil, cap.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > MaxMaterialBytes+64 {
		return nil, cap.ErrInvalid
	}
	f, e := r.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !os.SameFile(info, actual) {
		return nil, cap.ErrInvalid
	}
	data, e := io.ReadAll(io.LimitReader(f, MaxMaterialBytes+65))
	if e != nil {
		return nil, e
	}
	if len(data) < 45 || len(data) > MaxMaterialBytes+45 || data[0] != 1 {
		return nil, cap.ErrDecrypt
	}
	a, e := s.aead(ctx, data[1:17])
	if e != nil {
		return nil, e
	}
	v, e := a.Open(nil, data[17:29], data[29:], aad(id))
	if e != nil {
		return nil, cap.ErrDecrypt
	}
	if e = ctx.Err(); e != nil {
		clear(v)
		return nil, e
	}
	return v, nil
}
func (s *EncryptedFile) Create(ctx context.Context, id cap.ID, v []byte) error {
	if !valid(id) || len(v) == 0 || len(v) > MaxMaterialBytes {
		return cap.ErrInvalid
	}
	if e := s.slot(ctx); e != nil {
		return e
	}
	defer func() { <-s.slots }()
	r, e := s.root(true)
	if e != nil {
		return e
	}
	defer r.Close()
	name := filename(id)
	if _, e = r.Lstat(name); e == nil {
		return cap.ErrExists
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	header := make([]byte, 29)
	header[0] = 1
	if _, e = rand.Read(header[1:]); e != nil {
		return e
	}
	a, e := s.aead(ctx, header[1:17])
	if e != nil {
		return e
	}
	data := a.Seal(header, header[17:29], v, aad(id))
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return e
	}
	tmp := ".pending-" + hex.EncodeToString(nonce[:])
	f, e := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = r.Link(tmp, name); errors.Is(e, os.ErrExist) {
		return cap.ErrExists
	} else if e != nil {
		return e
	}
	d, e := r.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *EncryptedFile) Delete(ctx context.Context, id cap.ID) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !valid(id) {
		return cap.ErrInvalid
	}
	r, e := s.root(false)
	if e != nil {
		return e
	}
	defer r.Close()
	info, e := r.Lstat(filename(id))
	if errors.Is(e, os.ErrNotExist) {
		return cap.ErrNotFound
	}
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return cap.ErrInvalid
	}
	if e = r.Remove(filename(id)); e != nil {
		return e
	}
	d, e := r.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
