package network

import (
	"bytes"
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/libp2p/go-libp2p/core/crypto"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibp2pRequestIdentityAndLimits(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("wire")
	path := filepath.Join(t.TempDir(), "identity")
	a, err := NewLibp2p(Config{IdentityPath: path, Listen: []string{"/ip4/127.0.0.1/tcp/0"}, MaxMessageBytes: 1024}, Deps{Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}, MaxMessageBytes: 1024}, Deps{Protocol: p})
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
	reopened, err := NewLibp2p(Config{IdentityPath: path, Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: p})
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
	a, _ := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: testprotocol.New("a")})
	b, _ := NewLibp2p(Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}}, Deps{Protocol: testprotocol.New("b")})
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
	a, _ := NewLibp2p(Config{Timeout: "2s"}, Deps{Protocol: p})
	b, _ := NewLibp2p(Config{Timeout: "2s"}, Deps{Protocol: p})
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
	a, _ := NewLibp2p(Config{}, Deps{Protocol: p})
	b, _ := NewLibp2p(Config{}, Deps{Protocol: otherGenesis{p}})
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
	n, err := NewLibp2p(Config{Timeout: "10ms"}, Deps{Protocol: testprotocol.New("slots")})
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

func TestConcurrentIdentityCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-key")
	start := make(chan struct{})
	results := make(chan []byte, 64)
	failures := make(chan error, 64)
	for i := 0; i < 64; i++ {
		go func() {
			<-start
			k, err := identity(path)
			if err != nil {
				failures <- err
				return
			}
			data, err := crypto.MarshalPrivateKey(k)
			if err != nil {
				failures <- err
				return
			}
			results <- data
		}()
	}
	close(start)
	var first []byte
	for i := 0; i < 64; i++ {
		select {
		case err := <-failures:
			t.Error(err)
		case key := <-results:
			if first == nil {
				first = key
			} else if !bytes.Equal(first, key) {
				t.Error("concurrent creators got different identities")
			}
		}
	}
}

func TestInterruptedIdentityTempDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity")
	// A process dying while writing its private temporary file leaves no final name.
	if err := os.WriteFile(filepath.Join(dir, ".identity-abandoned"), []byte("partial-key"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := identity(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := crypto.UnmarshalPrivateKey(data)
	if err != nil || !stored.Equals(key) {
		t.Fatal("identity not fully published", err)
	}
	// A failed publication must preserve the winner and remove its private temp.
	if err := publishIdentity(path, []byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	reread, _ := os.ReadFile(path)
	if !bytes.Equal(data, reread) {
		t.Fatal("winner was overwritten")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".identity-*"))
	if len(matches) != 1 {
		t.Fatal("failed publication left temporary files", matches)
	}
}
