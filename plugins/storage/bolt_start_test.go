package storage

import (
	"context"
	"errors"
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type baseProtocol interface{ core.Protocol }
type changedGenesis struct{ baseProtocol }

func (p changedGenesis) Genesis(ctx context.Context) (core.Block, map[string][]byte, error) {
	b, _, e := p.baseProtocol.Genesis(ctx)
	if e != nil {
		return nil, nil, e
	}
	v, _ := protocol.UnwrapBlock(b)
	state := map[string][]byte{"initial": []byte("changed")}
	v.Header.StateRoot = protocol.StateRoot(state)
	b, e = protocol.WrapBlock(v)
	return b, state, e
}
func TestChangedGenesisRejectedAfterCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	parent, b, r := candidate(t, s)
	if e := s.Commit(ctx, parent, b, r); e != nil {
		t.Fatal(e)
	}
	s.Stop(ctx)
	next, _ := NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: changedGenesis{mustProtocol("dev")}})
	if e := next.Start(ctx); e == nil {
		next.Stop(ctx)
		t.Fatal("accepted changed genesis with same descriptor")
	}
}

type barrierProtocol struct {
	baseProtocol
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	fail    bool
}

func (p *barrierProtocol) StateCommitment(ctx context.Context, state map[string][]byte) (core.Commitment, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == 2 {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if p.fail {
			return "", errors.New("recovery failed")
		}
	}
	return p.baseProtocol.StateCommitment(ctx, state)
}
func TestRecoveryNotPublished(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "stop"}[stop], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "chain.db")
			s := openStore(t, path)
			s.Stop(ctx)
			p := &barrierProtocol{baseProtocol: mustProtocol("dev"), entered: make(chan struct{}), release: make(chan struct{}), fail: !stop}
			s, _ = NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
			done := make(chan error, 1)
			go func() { done <- s.Start(ctx) }()
			<-p.entered
			if _, e := s.Head(ctx); !errors.Is(e, core.ErrClosed) {
				t.Errorf("unverified database visible: %v", e)
			}
			g, _, _ := p.baseProtocol.Genesis(ctx)
			if e := s.Commit(ctx, g.ID(), g, core.ExecutionResult{}); !errors.Is(e, core.ErrClosed) {
				t.Errorf("commit visible during recovery: %v", e)
			}
			var stopped chan error
			if stop {
				stopped = make(chan error, 1)
				go func() { stopped <- s.Stop(ctx) }()
				for {
					s.mu.RLock()
					isStopped := s.stopped
					s.mu.RUnlock()
					if isStopped {
						break
					}
					runtime.Gosched()
				}
			}
			close(p.release)
			e := <-done
			if !stop && e == nil {
				t.Error("expected validation failure")
			}
			if stop {
				if e := <-stopped; e != nil {
					t.Fatal(e)
				}
			}
			if _, e = s.Head(ctx); !errors.Is(e, core.ErrClosed) {
				t.Fatal("database published after failure or stop", e)
			}
		})
	}
}
func TestMissingGenesisMetadataRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	if e := s.db.Update(ctx, func(tx capstorage.Writer) error { return tx.Delete("meta", []byte("genesisID")) }); e != nil {
		t.Fatal(e)
	}
	s.Stop(ctx)
	s, _ = NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: mustProtocol("dev")})
	if e := s.Start(ctx); e == nil {
		s.Stop(ctx)
		t.Fatal("accepted missing genesis metadata")
	}
}
func TestRecoveryPublishesAfterValidation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chain.db")
	s := openStore(t, path)
	s.Stop(ctx)
	p := &barrierProtocol{baseProtocol: mustProtocol("dev"), entered: make(chan struct{}), release: make(chan struct{})}
	s, _ = NewKV(KVConfig{}, KVDeps{Backend: testbackend.Bolt(t, path), Protocol: p})
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	<-p.entered
	if _, e := s.Head(ctx); !errors.Is(e, core.ErrClosed) {
		t.Fatal(e)
	}
	close(p.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	defer s.Stop(ctx)
	if _, e := s.Head(ctx); e != nil {
		t.Fatal(e)
	}
}
