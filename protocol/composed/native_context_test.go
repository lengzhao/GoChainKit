package composed

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
)

type nativeHistory map[core.ID]core.Block

func (h nativeHistory) Receipt(context.Context, core.ID) (core.Receipt, error) {
	return core.Receipt{}, core.ErrNotFound
}

type countedNativeHistory struct {
	nativeHistory
	reads int
}

func (h *countedNativeHistory) Block(ctx context.Context, id core.ID) (core.Block, error) {
	h.reads++
	return h.nativeHistory.Block(ctx, id)
}

func (h nativeHistory) Protocol() core.ProtocolDescriptor     { return core.ProtocolDescriptor{} }
func (h nativeHistory) Head(context.Context) (core.ID, error) { return "", nil }
func (h nativeHistory) Block(_ context.Context, id core.ID) (core.Block, error) {
	b, ok := h[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	return b, nil
}

func TestNativeExecutionAncestorContext(t *testing.T) {
	h := nativeHistory{}
	var parent core.Block
	for i := uint64(0); i < 300; i++ {
		var previous []byte
		if parent != nil {
			previous = []byte(parent.ID())
		}
		b, err := Wrap(Envelope{Header: Header{Version: 1, Binding: make([]byte, 32), Parent: previous, Height: i, Timestamp: 1000 + int64(i)}})
		if err != nil {
			t.Fatal(err)
		}
		h[b.ID()] = b
		parent = b
	}
	candidate, _ := Wrap(Envelope{Header: Header{Version: 1, Binding: make([]byte, 32), Parent: []byte(parent.ID()), Height: 300, Timestamp: 1300}})
	e := executionContext{parent: parent, candidate: candidate, history: h}
	id, err := e.BlockHash(context.Background(), 299)
	if err != nil || id != parent.ID() {
		t.Fatalf("parent hash %v", err)
	}
	if id, err = e.BlockHash(context.Background(), 300); err != nil || id != "" {
		t.Fatal("candidate visible")
	}
	if id, err = e.BlockHash(context.Background(), 43); err != nil || id != "" {
		t.Fatal("old block visible")
	}
	if id, err = e.BlockHash(context.Background(), 44); err != nil || id == "" {
		t.Fatal("256th ancestor unavailable")
	}
	median, err := e.MedianTimePast(context.Background())
	if err != nil || median != 1294 {
		t.Fatalf("median %d: %v", median, err)
	}
	counted := &countedNativeHistory{nativeHistory: h}
	bounded := e
	bounded.history = executionHistory{chain: counted, parent: parent.ID()}
	if _, err = bounded.BlockHash(context.Background(), 44); err != nil {
		t.Fatal(err)
	}
	if counted.reads > 256 {
		t.Fatalf("ancestor query amplified into %d reads", counted.reads)
	}
	delete(h, core.ID(string(mustEnvelope(parent).Header.Parent)))
	if _, err = e.BlockHash(context.Background(), 298); err == nil {
		t.Fatal("history failure hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.BlockHash(ctx, 299); err != context.Canceled {
		t.Fatal("cancellation hidden")
	}
}
func mustEnvelope(b core.Block) Envelope { v, _ := Unwrap(b); return v }
