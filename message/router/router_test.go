package router

import (
	"context"
	"errors"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
	"time"
)

type loopNetwork struct {
	networkcap.Network
	handler func(context.Context, []byte) ([]byte, error)
}

func (n *loopNetwork) Protocol() core.ProtocolDescriptor {
	return core.ProtocolDescriptor{Name: "test", Version: "1", Network: "test"}
}
func (n *loopNetwork) Handle(h func(context.Context, []byte) ([]byte, error)) error {
	n.handler = h
	return nil
}
func (n *loopNetwork) Request(ctx context.Context, _ string, b []byte) ([]byte, error) {
	return n.handler(ctx, b)
}
func TestRoutingLimitsAndClose(t *testing.T) {
	n := &loopNetwork{}
	r, e := New(Config{MaxBytes: 32}, Deps{Network: n})
	if e != nil {
		t.Fatal(e)
	}
	reg, e := r.Register("echo/v1", func(_ context.Context, b []byte) ([]byte, error) { return b, nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Register("echo/v1", func(context.Context, []byte) ([]byte, error) { return nil, nil }); e == nil {
		t.Fatal("duplicate accepted")
	}
	if e = r.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	b, e := r.Request(context.Background(), "peer", "echo/v1", []byte("ok"))
	if e != nil || string(b) != "ok" {
		t.Fatal(string(b), e)
	}
	if _, e = r.Request(context.Background(), "peer", "unknown/v1", nil); e == nil {
		t.Fatal("unknown accepted")
	}
	if _, e = r.Request(context.Background(), "peer", "echo/v1", make([]byte, 33)); e == nil {
		t.Fatal("oversize accepted")
	}
	if e = reg.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Request(context.Background(), "peer", "echo/v1", nil); e == nil {
		t.Fatal("closed route accepted")
	}
	if e = r.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Register("late/v1", func(context.Context, []byte) ([]byte, error) { return nil, nil }); e == nil {
		t.Fatal("registration after stop")
	}
}
func TestShutdownCancelsAndJoins(t *testing.T) {
	n := &loopNetwork{}
	r, _ := New(Config{}, Deps{Network: n})
	entered := make(chan struct{})
	exited := make(chan struct{})
	r.Register("wait/v1", func(ctx context.Context, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return nil, ctx.Err()
	})
	r.Start(context.Background())
	done := make(chan error, 1)
	go func() { _, e := r.Request(context.Background(), "peer", "wait/v1", nil); done <- e }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := r.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-exited:
	default:
		t.Fatal("did not join")
	}
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

type blockingNetwork struct {
	loopNetwork
	entered chan struct{}
	release chan struct{}
}

func (n *blockingNetwork) Request(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	select {
	case n.entered <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-n.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestOutgoingConcurrencyBudget(t *testing.T) {
	n := &blockingNetwork{entered: make(chan struct{}, 4), release: make(chan struct{})}
	r, _ := New(Config{MaxConcurrent: 1}, Deps{Network: n})
	r.Start(context.Background())
	defer r.Stop(context.Background())
	done := make(chan error, 2)
	go func() { _, e := r.Request(context.Background(), "p", "test/v1", nil); done <- e }()
	<-n.entered
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, e := r.Request(ctx, "p", "test/v1", nil); done <- e }()
	select {
	case <-n.entered:
		t.Fatal("outgoing budget bypassed")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(n.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
