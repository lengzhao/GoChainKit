// Package authority implements height-based fork choice with a stable ID tie
// break. It makes no finality claim and is independent of authority signing.
package authority

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
)

type Choice struct{}

func (*Choice) ID() string { return "height-id/authority/v1" }
func (*Choice) Score(ctx context.Context, _ core.ChainStore, b composed.Envelope, parent []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if b.Header.Height > 0 && (len(parent) != 8 || binary.BigEndian.Uint64(parent) != b.Header.Height-1) {
		return nil, fmt.Errorf("invalid parent height score")
	}
	return binary.BigEndian.AppendUint64(nil, b.Header.Height), nil
}
func (*Choice) Prefer(ctx context.Context, _ core.ChainStore, a, b composed.Envelope, x, y []byte) (bool, error) {
	if e := ctx.Err(); e != nil {
		return false, e
	}
	if len(x) != 8 || len(y) != 8 || binary.BigEndian.Uint64(x) != a.Header.Height || binary.BigEndian.Uint64(y) != b.Header.Height {
		return false, fmt.Errorf("invalid height score")
	}
	if b.Header.Height != a.Header.Height {
		return b.Header.Height > a.Header.Height, nil
	}
	return bytes.Compare([]byte(b.ID()), []byte(a.ID())) < 0, nil
}
func (*Choice) Finalized(ctx context.Context, _ core.ChainStore, _ composed.Envelope) (core.ID, error) {
	return "", ctx.Err()
}
