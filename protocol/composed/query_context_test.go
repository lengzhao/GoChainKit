package composed

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
)

type queryHistory struct{ nativeHistory }

func (queryHistory) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "test", Version: "1", Network: "test"}
}
func TestAnchoredQueryContext(t *testing.T) {
	ctx := context.Background()
	h := queryHistory{nativeHistory{}}
	g, _ := Wrap(Envelope{Header: Header{Version: 1, Binding: make([]byte, 32), Timestamp: 123}})
	h.nativeHistory[g.ID()] = g
	b, _ := Wrap(Envelope{Header: Header{Version: 1, Binding: make([]byte, 32), Parent: []byte(g.ID()), Height: 1, Timestamp: 456}})
	h.nativeHistory[b.ID()] = b
	p := &Protocol{descriptor: h.Protocol()}
	c, err := p.QueryContext(ctx, h, b.ID())
	if err != nil {
		t.Fatal(err)
	}
	if c.(model.HeightContext).Height() != 1 || c.(model.TimestampContext).Timestamp() != 456 {
		t.Fatal("query context not anchored")
	}
	id, err := c.(model.BlockHashContext).BlockHash(ctx, 0)
	if err != nil || id != g.ID() {
		t.Fatal("wrong query ancestor")
	}
	c, err = p.QueryContext(ctx, h, g.ID())
	if err != nil {
		t.Fatal(err)
	}
	if c.(model.TimestampContext).Timestamp() != 123 {
		t.Fatal("genesis timestamp lost")
	}
}
