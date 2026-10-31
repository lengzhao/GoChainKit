package height

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
)

type block struct {
	id, parent core.ID
	height     uint64
}

func (b block) ID() core.ID { return b.id }

type protocol struct{ core.BlockCodec }

func (protocol) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "height", Version: "1", Network: "test"}
}
func (protocol) Parent(b core.Block) (core.ID, error)     { return b.(block).parent, nil }
func (protocol) BlockHeight(b core.Block) (uint64, error) { return b.(block).height, nil }

type ledger struct {
	core.ChainStore
	blocks map[core.ID]block
	owner  *core.LedgerIdentity
}

func (l ledger) Protocol() core.ProtocolDescriptor     { return protocol{}.Protocol() }
func (l ledger) LedgerID() *core.LedgerIdentity        { return l.owner }
func (l ledger) Head(context.Context) (core.ID, error) { panic("must not read canonical head") }
func (l ledger) Block(ctx context.Context, id core.ID) (core.Block, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	b, ok := l.blocks[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return b, nil
}
func TestHeightAnchoredSideBranch(t *testing.T) {
	l := ledger{blocks: map[core.ID]block{"g": {"g", "", 0}, "a": {"a", "g", 1}, "b": {"b", "g", 1}, "tip": {"tip", "b", 2}}, owner: core.NewLedgerIdentity()}
	q, e := New(Config{}, Deps{Protocol: protocol{}, Ledger: l})
	if e != nil {
		t.Fatal(e)
	}
	b, e := q.AtHeight(context.Background(), "tip", 1)
	if e != nil || b.ID() != "b" {
		t.Fatal(b, e)
	}
	for _, c := range []struct {
		id core.ID
		h  uint64
	}{{"absent", 0}, {"a", 2}, {"", 0}} {
		if _, e = q.AtHeight(context.Background(), c.id, c.h); !errors.Is(e, core.ErrNotFound) {
			t.Fatal(c, e)
		}
	}
	l.blocks["tip"] = block{"tip", "b", 3}
	if _, e = q.AtHeight(context.Background(), "tip", 1); e == nil {
		t.Fatal("bad height accepted")
	}
}
