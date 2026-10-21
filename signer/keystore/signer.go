// Package keystore adapts an exportable local key store to a signer capability.
package keystore

import (
	"context"
	"fmt"
	store "github.com/lengzhao/GoChainKit/cap/keystore"
	cap "github.com/lengzhao/GoChainKit/cap/signer"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
)

type Config struct {
	KeyID store.ID `json:"keyID"`
}
type Deps struct {
	Store store.Store `json:"store"`
}
type Signer struct {
	store store.Store
	id    store.ID
}

func New(c Config, d Deps) (*Signer, error) {
	if d.Store == nil || c.KeyID == "" {
		return nil, fmt.Errorf("signer: store and key ID required")
	}
	return &Signer{store: d.Store, id: c.KeyID}, nil
}

// storeError intentionally hides store messages which might contain secrets,
// while retaining the original cause and its complete error tree for callers.
type storeError struct{ cause error }

func (e storeError) Error() string { return "signer: key store read failed" }
func (e storeError) Unwrap() error { return e.cause }
func (s *Signer) read(ctx context.Context) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	k, e := s.store.Get(ctx, s.id)
	if e != nil {
		clear(k)
		return nil, storeError{cause: e}
	}
	if e = ctx.Err(); e != nil {
		clear(k)
		return nil, e
	}
	return k, nil
}
func (s *Signer) PublicKey(ctx context.Context) (cap.PublicKey, error) {
	k, e := s.read(ctx)
	if e != nil {
		return cap.PublicKey{}, e
	}
	defer clear(k)
	p, e := suite.PublicKey(k)
	if e != nil {
		return cap.PublicKey{}, e
	}
	if e = ctx.Err(); e != nil {
		return cap.PublicKey{}, e
	}
	return p, nil
}
func (s *Signer) Sign(ctx context.Context, d cap.Domain, p []byte) ([]byte, error) {
	k, e := s.read(ctx)
	if e != nil {
		return nil, e
	}
	defer clear(k)
	sig, e := suite.Sign(k, d, p)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return sig, nil
}
