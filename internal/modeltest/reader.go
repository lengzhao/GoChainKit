package modeltest

import (
	"bytes"
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

type Reader map[string][]byte

func (r Reader) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := r[string(key)]
	if !ok {
		return nil, core.ErrNotFound
	}
	return bytes.Clone(value), nil
}
func (r Reader) Entries(ctx context.Context) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for k, v := range r {
		out[k] = bytes.Clone(v)
	}
	return out, nil
}
func (r Reader) Root() core.Commitment { return "" }
func (r Reader) Close() error          { return nil }
