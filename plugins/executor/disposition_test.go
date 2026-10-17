package executor

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"testing"
)

type nonceSnapshot struct {
	snapshot
	state map[string][]byte
}

func (s nonceSnapshot) Entries(context.Context) (map[string][]byte, error) { return s.state, nil }
func TestNonceDispositions(t *testing.T) {
	for _, test := range []struct {
		name            string
		nonce, previous uint64
		want            core.TransactionDisposition
	}{{"gap", 2, 0, core.TransactionDeferred}, {"stale", 1, 1, core.TransactionRejected}} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			p, _ := kv.New(kv.Config{ChainID: "test"})
			e, err := NewKV(KVConfig{}, Deps{Protocol: p})
			if err != nil {
				t.Fatal(err)
			}
			parent, _, _ := p.Genesis(ctx)
			raw := kv.Transaction{ChainID: "test", Nonce: test.nonce, Key: []byte("k")}
			if err := raw.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
				t.Fatal(err)
			}
			tx, err := kv.WrapTransaction(raw)
			if err != nil {
				t.Fatal(err)
			}
			draft, err := p.Draft(ctx, parent, []core.Transaction{tx})
			if err != nil {
				t.Fatal(err)
			}
			state := map[string][]byte{}
			if test.previous > 0 {
				encoded := make([]byte, 8)
				binary.BigEndian.PutUint64(encoded, test.previous)
				state[nonceKey(raw.Sender())] = encoded
			}
			_, err = e.Execute(ctx, nonceSnapshot{state: state}, draft)
			var disposition *core.TransactionError
			if !errors.As(err, &disposition) || disposition.TransactionID != tx.ID() || disposition.Disposition != test.want {
				t.Fatalf("expected typed disposition %v, got %v", test.want, err)
			}
		})
	}
}
