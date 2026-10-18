package network

import (
	"bytes"
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	identity "github.com/lengzhao/GoChainKit/identity/libp2p"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/keystore"
	"path/filepath"
	"testing"
	"time"
)

func TestLibp2pRequestIdentityAndLimits(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("wire")
	directory := filepath.Join(t.TempDir(), "keys")
	store, err := keystore.NewEncryptedFile(keystore.FileConfig{Directory: directory}, testSecret{})
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := identity.New(identity.Config{KeyID: "p2p"}, identity.Deps{Store: store})
	a, err := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}, MaxMessageBytes: 1024}, Deps{Protocol: p, Identity: provider})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}, MaxMessageBytes: 1024}, Deps{Protocol: p, Identity: testIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Handle(func(_ context.Context, in []byte) ([]byte, error) { return append([]byte("echo:"), in...), nil }); err != nil {
		t.Fatal(err)
	}
	for _, n := range []interface {
		Start(context.Context) error
		Stop(context.Context) error
	}{a, b} {
		if err := n.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { n.Stop(ctx) })
	}
	if err := a.Connect(ctx, b.Addresses()[0]); err != nil {
		t.Fatal(err)
	}
	peers := a.Peers()
	if len(peers) != 1 {
		t.Fatal(peers)
	}
	out, err := a.Request(ctx, peers[0], []byte("hello"))
	if err != nil || string(out) != "echo:hello" {
		t.Fatal(string(out), err)
	}
	if _, err := a.Request(ctx, peers[0], bytes.Repeat([]byte{1}, 1025)); err == nil {
		t.Fatal("oversized message accepted")
	}
	old := a.Addresses()[0]
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	store2, _ := keystore.NewEncryptedFile(keystore.FileConfig{Directory: directory}, testSecret{})
	reopenedProvider, _ := identity.New(identity.Config{KeyID: "p2p"}, identity.Deps{Store: store2})
	reopened, err := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: p, Identity: reopenedProvider})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop(ctx)
	if peerPart(old) != peerPart(reopened.Addresses()[0]) {
		t.Fatal("identity changed")
	}
}
func TestForeignChainRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, _ := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: testprotocol.New("a"), Identity: testIdentity()})
	b, _ := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: testprotocol.New("b"), Identity: testIdentity()})
	b.Handle(func(context.Context, []byte) ([]byte, error) {
		t.Error("foreign chain reached handler")
		return nil, nil
	})
	for _, n := range []*Transport{a, b} {
		if err := n.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer n.Stop(context.Background())
	}
	if err := a.Connect(ctx, b.Addresses()[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Request(ctx, a.Peers()[0], []byte("hi")); err == nil {
		t.Fatal("wrong network accepted")
	}
}

func TestRequestCancellationAndHandlerDrain(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("cancel")
	a, _ := NewLibp2p(Config{Timeout: "2s"}, Deps{Protocol: p, Identity: testIdentity()})
	b, _ := NewLibp2p(Config{Timeout: "2s"}, Deps{Protocol: p, Identity: testIdentity()})
	entered := make(chan struct{})
	drained := make(chan struct{})
	b.Handle(func(ctx context.Context, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(drained)
		return nil, ctx.Err()
	})
	for _, n := range []*Transport{a, b} {
		if err := n.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer n.Stop(context.Background())
	}
	if err := a.Connect(ctx, b.Addresses()[0]); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := a.Request(requestCtx, a.Peers()[0], nil); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler not reached")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("request did not cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not reset stream")
	}
	if err := b.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-drained:
	default:
		t.Fatal("handler leaked on Stop")
	}
}

type otherGenesis struct{ *testprotocol.Format }

func (p otherGenesis) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	b, s, err := p.Format.Genesis(ctx)
	frame := b.(testprotocol.Frame)
	frame.OtherLinks = []string{"different-genesis"}
	return frame, s, err
}
func TestSameDescriptorDifferentGenesisRejected(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("same")
	a, _ := NewLibp2p(Config{}, Deps{Protocol: p, Identity: testIdentity()})
	b, _ := NewLibp2p(Config{}, Deps{Protocol: otherGenesis{p}, Identity: testIdentity()})
	b.Handle(func(context.Context, []byte) ([]byte, error) {
		t.Error("foreign genesis reached handler")
		return nil, nil
	})
	for _, n := range []*Transport{a, b} {
		if err := n.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer n.Stop(ctx)
	}
	if err := a.Connect(ctx, b.Addresses()[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Request(ctx, a.Peers()[0], nil); err == nil {
		t.Fatal("foreign genesis accepted")
	}
}
func TestOutgoingSaturationCancellation(t *testing.T) {
	n, err := NewLibp2p(Config{Timeout: "10ms"}, Deps{Protocol: testprotocol.New("slots"), Identity: testIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(n.outgoing); i++ {
		n.outgoing <- struct{}{}
	}
	started := time.Now()
	if _, err := n.Request(context.Background(), "unused", nil); err == nil {
		t.Fatal("saturation did not fail")
	}
	if time.Since(started) > time.Second {
		t.Fatal("saturated queue ignored timeout")
	}
}

func testIdentity() *identity.Provider {
	p, _ := identity.New(identity.Config{KeyID: "p2p"}, identity.Deps{Store: keystore.NewMemory()})
	return p
}
func TestMissingIdentity(t *testing.T) {
	if _, e := NewLibp2p(Config{}, Deps{Protocol: testprotocol.New("missing")}); e == nil {
		t.Fatal("missing identity accepted")
	}
}

type testSecret struct{}

func (testSecret) Secret(context.Context) ([]byte, error) {
	return []byte("network-test-password"), nil
}

type failedIdentity struct {
	data []byte
	err  error
}

func (p failedIdentity) PrivateKey(context.Context) ([]byte, error) { return p.data, p.err }
func TestIdentityFailureClearsReturnedMaterial(t *testing.T) {
	material := []byte("private-key-material")
	failure := errors.New("identity unavailable")
	n, e := NewLibp2p(Config{}, Deps{Protocol: testprotocol.New("failed-identity"), Identity: failedIdentity{material, failure}})
	if e != nil {
		t.Fatal(e)
	}
	if e = n.Start(context.Background()); !errors.Is(e, failure) {
		t.Fatal(e)
	}
	if !bytes.Equal(material, make([]byte, len(material))) {
		t.Fatal("failed identity material retained")
	}
	if n.running || n.h != nil || len(n.Addresses()) != 0 {
		t.Fatal("failed identity started network")
	}
}
