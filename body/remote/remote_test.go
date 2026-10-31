package remote

import (
	"context"
	"encoding/json"
	"errors"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/routertest"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"testing"
	"time"
)

type net struct {
	networkcap.Network
	h func(context.Context, []byte) ([]byte, error)
	p *testprotocol.Format
}

func (n *net) Protocol() core.ProtocolDescriptor                               { return n.p.Protocol() }
func (n *net) Handle(h func(context.Context, []byte) ([]byte, error)) error    { n.h = h; return nil }
func (n *net) Peers() []string                                                 { return []string{"peer"} }
func (n *net) Request(ctx context.Context, _ string, b []byte) ([]byte, error) { return n.h(ctx, b) }

type source struct {
	*testprotocol.Format
	tx core.Transaction
}

func (s source) Transaction(_ context.Context, id core.ID) (core.Transaction, error) {
	if s.tx != nil && s.tx.ID() == id {
		return s.tx, nil
	}
	return nil, core.ErrNotFound
}
func TestBodyRouteRoundTripAndMissing(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("remote")
	n := &net{p: p}
	r := routertest.Start(t, n)
	tx := testprotocol.Tx{Label: "one"}
	server, e := NewServer(Config{}, ServerDeps{Protocol: p, Source: source{p, tx}, Router: r})
	if e != nil {
		t.Fatal(e)
	}
	if e = server.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer server.Stop(ctx)
	client, e := New(Config{}, Deps{Protocol: p, Network: n, Router: r})
	if e != nil {
		t.Fatal(e)
	}
	got, e := client.Transaction(ctx, tx.ID())
	if e != nil || got.ID() != tx.ID() {
		t.Fatal(got, e)
	}
	if _, e = client.Transaction(ctx, "absent"); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
}

type brokenSource struct {
	*testprotocol.Format
	failure error
}

func (s brokenSource) Transaction(context.Context, core.ID) (core.Transaction, error) {
	return nil, s.failure
}
func TestBodyServerSupervisesLocalFailure(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("fault")
	n := &net{p: p}
	r := routertest.Start(t, n)
	disk := errors.New("disk failed")
	s, _ := NewServer(Config{}, ServerDeps{Protocol: p, Source: brokenSource{p, disk}, Router: r})
	s.Start(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	raw, _ := json.Marshal(request{ID: []byte("tx")})
	if _, e := r.Request(ctx, "peer", Route, raw); !errors.Is(e, disk) {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, disk) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("local fault not supervised")
	}
}

func TestServingBudgetRejectionDoesNotFailNode(t *testing.T) {
	p := testprotocol.New("budget")
	n := &net{p: p}
	r := routertest.Start(t, n)
	tx := testprotocol.Tx{Label: "valid"}
	s, _ := NewServer(Config{MaxTransactionBytes: 1}, ServerDeps{Protocol: p, Source: source{p, tx}, Router: r})
	s.Start(context.Background())
	defer s.Stop(context.Background())
	raw, _ := json.Marshal(request{ID: []byte(tx.ID())})
	if _, e := r.Request(context.Background(), "peer", Route, raw); !errors.Is(e, core.ErrFull) {
		t.Fatal(e)
	}
	select {
	case <-s.fatal:
		t.Fatal("request budget rejection stopped node")
	default:
	}
}
