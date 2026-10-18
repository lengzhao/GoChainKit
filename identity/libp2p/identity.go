// Package libp2p adapts exportable local keys to libp2p identity.
package libp2p

import (
	"context"
	"crypto/rand"
	"fmt"
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"github.com/libp2p/go-libp2p/core/crypto"
)

type Config struct {
	KeyID cap.ID `json:"keyID"`
}
type Deps struct {
	Store cap.Store `json:"store"`
}
type Provider struct {
	store cap.Store
	id    cap.ID
}

func New(c Config, d Deps) (*Provider, error) {
	if d.Store == nil || len(c.KeyID) == 0 {
		return nil, fmt.Errorf("identity: store and key ID required")
	}
	return &Provider{d.Store, c.KeyID}, nil
}
func validate(v []byte) ([]byte, error) {
	if _, e := crypto.UnmarshalPrivateKey(v); e != nil {
		clear(v)
		return nil, fmt.Errorf("identity: invalid stored private key")
	}
	return v, nil
}

// pure accepts only wrappers of a single sentinel, never joined local faults.
func pure(e, target error) bool {
	if e == target {
		return true
	}
	if u, ok := e.(interface{ Unwrap() error }); ok {
		return pure(u.Unwrap(), target)
	}
	return false
}
func (p *Provider) PrivateKey(ctx context.Context) ([]byte, error) {
	v, e := p.store.Get(ctx, p.id)
	if e == nil {
		return validate(v)
	}
	clear(v)
	if !pure(e, cap.ErrNotFound) {
		return nil, e
	}
	key, _, e := crypto.GenerateEd25519Key(rand.Reader)
	if e != nil {
		return nil, e
	}
	v, e = crypto.MarshalPrivateKey(key)
	if e != nil {
		return nil, e
	}
	if e = p.store.Create(ctx, p.id, v); e != nil {
		clear(v)
		if pure(e, cap.ErrExists) {
			v, e = p.store.Get(ctx, p.id)
			if e == nil {
				return validate(v)
			}
		}
		clear(v)
		return nil, e
	}
	return v, nil
}
