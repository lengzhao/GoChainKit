package storage

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"testing"
)

type canceledHydration struct{ *testprotocol.Format }

func (canceledHydration) Materialize(context.Context, core.Block) (core.Block, error) {
	return nil, context.Canceled
}
func TestHydrationCancellationIsNotDataCorruption(t *testing.T) {
	p := testprotocol.New("cancel")
	g, _, _ := p.Genesis(context.Background())
	raw, _ := p.EncodeBlock(g)
	_, e := decodeAvailableBlock(context.Background(), p, raw, g.ID(), &bodyAccess{materializer: canceledHydration{p}})
	if e != context.Canceled {
		t.Fatal(e)
	}
}
