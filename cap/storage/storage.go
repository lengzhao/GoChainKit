// Package storage defines transactional named key/value storage independent of chain semantics.
package storage

import (
	"context"
	"errors"
)

var (
	ErrNotFound = errors.New("storage key or namespace not found")
	ErrClosed   = errors.New("storage database closed")
	ErrExpired  = errors.New("storage transaction expired")
	// ErrCommitUnknown marks a write whose durable outcome cannot be established.
	// Callback and cancellation failures roll back and never carry this marker.
	ErrCommitUnknown = errors.New("storage commit outcome unknown")
)

// Backend is a reusable configuration factory. Each Open returns an owned database
// handle; factories never silently share a mutable in-memory database.
type Backend interface {
	Open(context.Context) (Database, error)
}

// Database callbacks observe one consistent view. Update commits atomically only
// when both callback and context succeed. Panics roll back and are propagated.
// Handles expire on callback return (including panic). Callbacks must not invoke
// Database operations on this same database, nor retain handles for asynchronous
// work. Close waits for active callbacks; it must not be called inside a callback.
type Database interface {
	View(context.Context, func(Reader) error) error
	Update(context.Context, func(Writer) error) error
	Close() error
}

// Reader returns owned bytes. Scan visits keys in ascending byte order filtered
// by prefix. Scan takes an owned snapshot of the selected entries before invoking callbacks.
type Reader interface {
	Namespaces() ([]string, error)
	HasNamespace(string) (bool, error)
	Get(string, []byte) ([]byte, error)
	Scan(string, []byte, func([]byte, []byte) error) error
}
type Writer interface {
	Reader
	CreateNamespace(string) error
	Put(string, []byte, []byte) error
	Delete(string, []byte) error
}
