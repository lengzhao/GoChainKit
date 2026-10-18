package libp2p

import (
	"bytes"
	"context"
	"errors"
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"github.com/lengzhao/GoChainKit/keystore"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentCreators(t *testing.T) {
	s := keystore.NewMemory()
	p, _ := New(Config{KeyID: "p2p"}, Deps{Store: s})
	var wg sync.WaitGroup
	out := make(chan []byte, 64)
	for range 64 {
		wg.Go(func() {
			v, e := p.PrivateKey(context.Background())
			if e != nil {
				t.Error(e)
			}
			out <- v
		})
	}
	wg.Wait()
	close(out)
	var first []byte
	for v := range out {
		if first == nil {
			first = v
		} else if !bytes.Equal(first, v) {
			t.Fatal("different keys published")
		}
	}
}

type faultStore struct {
	err     error
	created bool
}

func (s *faultStore) Get(context.Context, cap.ID) ([]byte, error)  { return nil, s.err }
func (s *faultStore) Create(context.Context, cap.ID, []byte) error { s.created = true; return nil }
func (s *faultStore) Delete(context.Context, cap.ID) error         { return nil }
func TestMixedMissingDoesNotGenerate(t *testing.T) {
	fault := errors.New("disk failed")
	s := &faultStore{err: errors.Join(cap.ErrNotFound, fault)}
	p, _ := New(Config{KeyID: "p2p"}, Deps{Store: s})
	_, e := p.PrivateKey(context.Background())
	if !errors.Is(e, fault) || s.created {
		t.Fatal(e, s.created)
	}
}
func TestInvalidStoredDoesNotReplace(t *testing.T) {
	s := keystore.NewMemory()
	s.Create(context.Background(), "p2p", []byte("invalid"))
	p, _ := New(Config{KeyID: "p2p"}, Deps{Store: s})
	if _, e := p.PrivateKey(context.Background()); e == nil {
		t.Fatal("invalid key accepted")
	}
	v, _ := s.Get(context.Background(), "p2p")
	if string(v) != "invalid" {
		t.Fatal("replaced")
	}
}

type arbitraryIDStore struct {
	id   cap.ID
	data []byte
}

func (s arbitraryIDStore) Get(_ context.Context, id cap.ID) ([]byte, error) {
	if id != s.id {
		return nil, cap.ErrNotFound
	}
	return bytes.Clone(s.data), nil
}
func (s arbitraryIDStore) Create(context.Context, cap.ID, []byte) error { return cap.ErrExists }
func (s arbitraryIDStore) Delete(context.Context, cap.ID) error         { return nil }
func TestProviderLeavesIdentifierLimitsToStore(t *testing.T) {
	p, _ := New(Config{KeyID: "source"}, Deps{Store: keystore.NewMemory()})
	material, e := p.PrivateKey(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	longID := cap.ID(strings.Repeat("reference/", 100))
	provider, e := New(Config{KeyID: longID}, Deps{Store: arbitraryIDStore{longID, material}})
	if e != nil {
		t.Fatal(e)
	}
	got, e := provider.PrivateKey(context.Background())
	if e != nil || !bytes.Equal(got, material) {
		t.Fatal(e)
	}
}
