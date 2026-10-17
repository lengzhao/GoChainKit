package single

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core/contracts"
	"sync/atomic"
	"testing"
	"time"
)

type host struct {
	proposed  atomic.Int32
	committed atomic.Int32
	fail      error
	done      chan struct{}
}

func (h *host) Propose(context.Context) (contracts.Block, error) {
	h.proposed.Add(1)
	return testBlock{}, nil
}
func (h *host) Validate(context.Context, contracts.Block) error { return h.fail }
func (h *host) Commit(context.Context, contracts.Block) error {
	if h.committed.Add(1) == 2 {
		close(h.done)
	}
	return nil
}
func TestProducesAndStops(t *testing.T) {
	c, err := New(Config{Interval: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &host{done: make(chan struct{})}
	exited := make(chan error, 1)
	go func() { exited <- c.Run(ctx, h) }()
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("did not produce blocks")
	}
	cancel()
	select {
	case err := <-exited:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not stop")
	}
}
func TestValidationFailureStopsWithoutCommit(t *testing.T) {
	c, err := New(Config{Interval: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("invalid proposal")
	h := &host{fail: failure}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Run(ctx, h); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if h.committed.Load() != 0 {
		t.Fatal("committed invalid block")
	}
}

type testBlock struct{}

func (testBlock) ID() contracts.ID { return "test" }
