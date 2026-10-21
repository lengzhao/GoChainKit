package authority

import (
	"context"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"testing"
)

func TestHeightTieBreakAndFinality(t *testing.T) {
	ctx := context.Background()
	c := &Choice{}
	g := composed.Envelope{}
	gs, _ := c.Score(ctx, nil, g, nil)
	a := composed.Envelope{Header: composed.Header{Height: 1, Timestamp: 1}}
	b := composed.Envelope{Header: composed.Header{Height: 1, Timestamp: 2}}
	as, e := c.Score(ctx, nil, a, gs)
	if e != nil {
		t.Fatal(e)
	}
	bs, _ := c.Score(ctx, nil, b, gs)
	ab, e := c.Prefer(ctx, nil, a, b, as, bs)
	if e != nil {
		t.Fatal(e)
	}
	ba, _ := c.Prefer(ctx, nil, b, a, bs, as)
	if ab == ba {
		t.Fatal("tie order unstable")
	}
	if id, e := c.Finalized(ctx, nil, a); e != nil || id != "" {
		t.Fatal("false finality", e)
	}
	if _, e = c.Score(ctx, nil, b, nil); e == nil {
		t.Fatal("missing parent score accepted")
	}
}
