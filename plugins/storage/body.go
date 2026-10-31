package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	"github.com/lengzhao/GoChainKit/core"
)

// bodyAccess is configured only for reference protocols. Its materializer must
// use local durable sources: ledger reads and startup never fetch from peers.
type bodyAccess struct {
	archive      bodycap.TransactionArchive
	materializer bodycap.Materializer
}

func newBodyAccess(p core.LedgerProtocol, a bodycap.TransactionArchive, m bodycap.Materializer) (*bodyAccess, error) {
	_, required := p.(bodycap.References)
	if mode, ok := p.(bodycap.Requirement); ok {
		required = mode.RequiresMaterialization()
	}
	if !required {
		if a != nil || m != nil {
			return nil, fmt.Errorf("body dependencies require a reference protocol")
		}
		return nil, nil
	}
	if a == nil || m == nil {
		return nil, fmt.Errorf("reference ledger requires archive and local materializer")
	}
	if e := core.RequireCompatible(p.Protocol(), a, m); e != nil {
		return nil, e
	}
	return &bodyAccess{a, m}, nil
}
func firstBody(access []*bodyAccess) *bodyAccess {
	if len(access) > 0 {
		return access[0]
	}
	return nil
}
func decodeAvailableBlock(ctx context.Context, p core.LedgerProtocol, data []byte, id core.ID, access ...*bodyAccess) (core.Block, error) {
	b, e := decodeStoredBlock(p, data, id)
	if e != nil {
		return nil, e
	}
	if a := firstBody(access); a != nil {
		b, e = a.materializer.Materialize(ctx, b)
		if e != nil {
			if bodyCancellation(e) {
				return nil, e
			}
			return nil, errors.Join(bodycap.ErrUnavailable, e)
		}
		if b == nil || b.ID() != id {
			return nil, fmt.Errorf("materialized block identity mismatch")
		}
		raw, e := p.EncodeBlock(b)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(raw, data) {
			return nil, fmt.Errorf("materialized block encoding mismatch")
		}
	}
	return b, nil
}

func bodyCancellation(e error) bool {
	if e == context.Canceled || e == context.DeadlineExceeded {
		return true
	}
	if many, ok := e.(interface{ Unwrap() []error }); ok {
		if len(many.Unwrap()) == 0 {
			return false
		}
		for _, child := range many.Unwrap() {
			if !bodyCancellation(child) {
				return false
			}
		}
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return bodyCancellation(w.Unwrap())
	}
	return false
}
