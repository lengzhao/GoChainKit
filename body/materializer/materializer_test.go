package materializer

import (
	"context"
	"errors"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"testing"
)

type refBlock struct {
	ids []core.ID
	txs []core.Transaction
}

func (b refBlock) ID() core.ID { return "block" }

type refs struct{ *testprotocol.Format }

func (p refs) References(b core.Block) ([]core.ID, error) { return b.(refBlock).ids, nil }
func (p refs) Attach(b core.Block, txs []core.Transaction) (core.Block, error) {
	v := b.(refBlock)
	v.txs = txs
	return v, nil
}

type source struct {
	*testprotocol.Format
	tx  core.Transaction
	err error
}

func (s source) Transaction(context.Context, core.ID) (core.Transaction, error) { return s.tx, s.err }
func TestMaterializeIdentityBudgetAndErrors(t *testing.T) {
	p := testprotocol.New("body")
	tx := testprotocol.Tx{Label: "one"}
	b := refBlock{ids: []core.ID{tx.ID()}}
	m, e := New(Config{}, Deps{References: refs{p}, Protocol: p, Sources: []bodycap.TransactionSource{source{Format: p, err: core.ErrNotFound}, source{Format: p, tx: tx}}})
	if e != nil {
		t.Fatal(e)
	}
	got, e := m.Materialize(context.Background(), b)
	if e != nil || len(got.(refBlock).txs) != 1 {
		t.Fatal(got, e)
	}
	m, _ = New(Config{}, Deps{References: refs{p}, Protocol: p, Sources: []bodycap.TransactionSource{source{Format: p, tx: testprotocol.Tx{Label: "wrong"}}}})
	if _, e = m.Materialize(context.Background(), b); e == nil {
		t.Fatal("wrong id")
	}
	failure := errors.New("disk")
	m, _ = New(Config{}, Deps{References: refs{p}, Protocol: p, Sources: []bodycap.TransactionSource{source{Format: p, err: errors.Join(core.ErrNotFound, failure)}, source{Format: p, tx: tx}}})
	if _, e = m.Materialize(context.Background(), b); !errors.Is(e, failure) {
		t.Fatal("masked failure", e)
	}
	m, _ = New(Config{MaxBytes: 1}, Deps{References: refs{p}, Protocol: p, Sources: []bodycap.TransactionSource{source{Format: p, tx: tx}}})
	if _, e = m.Materialize(context.Background(), b); !errors.Is(e, core.ErrFull) {
		t.Fatal("budget", e)
	}
}

type slowSource struct{ *testprotocol.Format }

func (s slowSource) Transaction(ctx context.Context, _ core.ID) (core.Transaction, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestWholeBodyTimeout(t *testing.T) {
	p := testprotocol.New("timeout")
	m, e := New(Config{Timeout: "5ms"}, Deps{References: refs{p}, Protocol: p, Sources: []bodycap.TransactionSource{slowSource{p}}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Materialize(context.Background(), refBlock{ids: []core.ID{"one"}})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
