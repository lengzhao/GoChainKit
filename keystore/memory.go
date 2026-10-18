// Package keystore implements optional storage for exportable local key material.
package keystore

import (
	"bytes"
	"context"
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"sync"
)

const MaxMaterialBytes = 64 << 10

func valid(id cap.ID) bool { return len(id) > 0 && len(id) <= 120 }

type Memory struct {
	mu   sync.RWMutex
	data map[cap.ID][]byte
}

func NewMemory() *Memory { return &Memory{data: map[cap.ID][]byte{}} }
func (s *Memory) Get(ctx context.Context, id cap.ID) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !valid(id) {
		return nil, cap.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[id]
	if !ok {
		return nil, cap.ErrNotFound
	}
	return bytes.Clone(v), nil
}
func (s *Memory) Create(ctx context.Context, id cap.ID, v []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !valid(id) || len(v) == 0 || len(v) > MaxMaterialBytes {
		return cap.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[id]; ok {
		return cap.ErrExists
	}
	s.data[id] = bytes.Clone(v)
	return nil
}
func (s *Memory) Delete(ctx context.Context, id cap.ID) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !valid(id) {
		return cap.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[id]
	if !ok {
		return cap.ErrNotFound
	}
	clear(v)
	delete(s.data, id)
	return nil
}
