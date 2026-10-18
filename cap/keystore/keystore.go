// Package keystore defines optional exportable local key material storage.
package keystore

import (
	"context"
	"errors"
)

type ID string

var (
	ErrNotFound = errors.New("key not found")
	ErrExists   = errors.New("key already exists")
	ErrDecrypt  = errors.New("key record authentication failed")
	ErrInvalid  = errors.New("invalid key record or identifier")
)

// Store returns caller-owned bytes. Create is exclusive and never replaces a key.
type Store interface {
	Get(context.Context, ID) ([]byte, error)
	Create(context.Context, ID, []byte) error
	Delete(context.Context, ID) error
}

// SecretSource returns caller-owned secret bytes, read only when required.
type SecretSource interface {
	Secret(context.Context) ([]byte, error)
}
