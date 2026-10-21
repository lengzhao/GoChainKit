package journal

import (
	"bytes"
	"context"
	"fmt"

	capstate "github.com/lengzhao/GoChainKit/cap/state"
	"github.com/lengzhao/GoChainKit/core"
)

// ByteReader is the narrow portion of core.StateReader used by the adapter.
// Entries, Root and Close are intentionally not required or called.
type ByteReader interface {
	Get(context.Context, []byte) ([]byte, error)
}
type reader struct{ base ByteReader }

// FromStateReader borrows a stable reader. The caller retains its lifecycle.
func FromStateReader(r ByteReader) (capstate.Reader, error) {
	if r == nil || isNil(r) {
		return nil, fmt.Errorf("journal: reader required")
	}
	return reader{r}, nil
}
func (r reader) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	v, err := r.base.Get(ctx, []byte(key))
	if err != nil {
		if onlyNotFound(err) {
			if e := ctx.Err(); e != nil {
				return nil, false, e
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	return bytes.Clone(v), true, nil
}

// Every error leaf must be the missing-key sentinel. A joined disk failure must
// never be turned into a missing key just because errors.Is matches NotFound.
func onlyNotFound(err error) bool {
	if err == core.ErrNotFound {
		return true
	}
	if e, ok := err.(interface{ Unwrap() []error }); ok {
		children := e.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyNotFound(child) {
				return false
			}
		}
		return true
	}
	if e, ok := err.(interface{ Unwrap() error }); ok {
		return onlyNotFound(e.Unwrap())
	}
	return false
}
