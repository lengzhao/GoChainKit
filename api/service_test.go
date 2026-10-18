package api_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	"github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"path/filepath"
	"testing"
)

func TestServiceAnchorsAndClosedLedger(t *testing.T) {
	p, _ := kv.New(kv.Config{ChainID: "api"})
	l, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: p})
	s, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: l, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, snap, err := s.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if a != h.Block.ID || snap.Root() != h.StateRoot {
		t.Fatal("unanchored snapshot")
	}
	if _, err = s.Transaction(context.Background(), core.ID(string([]byte{255, 0}))); err != core.ErrNotFound {
		t.Fatal(err)
	}
	if _, err = s.SubmitEncoded(context.Background(), []byte("bad")); !errors.Is(err, api.ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestAdmissionQueryRealProtocols(t *testing.T) {
	for _, kind := range []string{"memory", "bolt"} {
		for _, format := range []string{"kv", "flex"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				ctx := context.Background()
				var p core.Protocol
				var ex core.Executor
				var tx core.Transaction
				if format == "kv" {
					p, _ = kv.New(kv.Config{ChainID: "api-matrix"})
					ex, _ = kv.NewExecutor(p)
					native := kv.Transaction{ChainID: "api-matrix", Nonce: 1, Key: []byte("k"), Value: []byte{255, 0, 128}}
					if err := native.Sign(ed25519.NewKeyFromSeed(make([]byte, 32))); err != nil {
						t.Fatal(err)
					}
					tx, _ = kv.WrapTransaction(native)
				} else {
					p = testprotocol.New("api-matrix")
					ex = testprotocol.Executor{P: p}
					tx = testprotocol.Tx{Label: "one", Body: []byte{255, 0, 128}}
				}
				var ledger interface {
					api.Ledger
					core.Committer
				}
				if kind == "memory" {
					ledger, _ = storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
				} else {
					b, err := storage.NewBolt(storage.BoltConfig{Path: filepath.Join(t.TempDir(), "api.db")}, storage.Deps{Protocol: p})
					if err != nil {
						t.Fatal(err)
					}
					if err = b.Start(ctx); err != nil {
						t.Fatal(err)
					}
					defer b.Stop(ctx)
					ledger = b
				}
				pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 2 << 20}, mempool.Deps{Protocol: p})
				s, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ledger, Pool: pool})
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := p.EncodeTransaction(tx)
				admission, err := s.SubmitEncoded(ctx, raw)
				if err != nil || admission.Status != query.Accepted || admission.TransactionID != tx.ID() || admission.BlockID != "" {
					t.Fatalf("admit %+v %v", admission, err)
				}
				raw[0] ^= 255
				if _, err = s.Submit(ctx, tx); err != nil {
					t.Fatal("idempotent admission", err)
				}
				pending, err := s.Transaction(ctx, tx.ID())
				if err != nil || pending.Status != query.Accepted {
					t.Fatal(pending, err)
				}
				pending.Data[0] ^= 255
				pending, err = s.Transaction(ctx, tx.ID())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.DecodeTransaction(pending.Data); err != nil {
					t.Fatal("aliased pending bytes", err)
				}
				if _, err = s.Receipt(ctx, tx.ID()); err != core.ErrNotFound {
					t.Fatal("pending receipt", err)
				}
				anchor, snap, err := s.Snapshot(ctx, "")
				if err != nil {
					t.Fatal(err)
				}
				defer snap.Close()
				parent, err := ledger.Block(ctx, anchor)
				if err != nil {
					t.Fatal(err)
				}
				d, err := p.Draft(ctx, parent, []core.Transaction{tx})
				if err != nil {
					t.Fatal(err)
				}
				r, err := ex.Execute(ctx, snap, d)
				if err != nil {
					t.Fatal(err)
				}
				b, err := p.Finalize(ctx, d, r)
				if err != nil {
					t.Fatal(err)
				}
				r.BlockID = b.ID()
				if err = ledger.Commit(ctx, anchor, b, r); err != nil {
					t.Fatal(err)
				}
				if got, _ := ledger.Head(ctx); got == anchor {
					t.Fatal("commit did not advance")
				}
				if snap.Root() == r.StateRoot {
					t.Fatal("snapshot changed with head")
				}
				admission, err = s.Submit(ctx, tx)
				if err != nil || admission.Status != query.Confirmed || admission.BlockID != b.ID() || admission.Head != b.ID() {
					t.Fatal(admission, err)
				}
				confirmed, err := s.Transaction(ctx, tx.ID())
				if err != nil || confirmed.BlockID != b.ID() || confirmed.Head != b.ID() || confirmed.Status != query.Confirmed {
					t.Fatal(confirmed, err)
				}
				decoded, err := p.DecodeTransaction(confirmed.Data)
				if err != nil || decoded.ID() != tx.ID() {
					t.Fatal("binary ID roundtrip", err)
				}
				receipt, err := s.Receipt(ctx, tx.ID())
				if err != nil || receipt.BlockID != b.ID() || receipt.Head != b.ID() {
					t.Fatal(receipt, err)
				}
				if format == "flex" && receipt.Receipt != nil {
					t.Fatal("optional receipt fabricated")
				}
				if format == "kv" && receipt.Receipt == nil {
					t.Fatal("receipt omitted")
				}
				view, err := s.Block(ctx, b.ID())
				if err != nil {
					t.Fatal(err)
				}
				view.Data[0] ^= 255
				view, err = s.Block(ctx, b.ID())
				if err != nil {
					t.Fatal(err)
				}
				block, err := p.DecodeBlock(view.Data)
				if err != nil || block.ID() != b.ID() {
					t.Fatal("block alias/ID", err)
				}
				at, old, err := s.Snapshot(ctx, anchor)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				if at != anchor || old.Root() != snap.Root() {
					t.Fatal("historical snapshot anchor")
				}
			})
		}
	}
}
func TestAdmissionLimitsAndBadSignature(t *testing.T) {
	ctx := context.Background()
	p, _ := kv.New(kv.Config{ChainID: "admit"})
	l, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: p})
	s, _ := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: l, Pool: pool})
	signed := func(n uint64) core.Transaction {
		v := kv.Transaction{ChainID: "admit", Nonce: n, Key: []byte("k"), Value: []byte("v")}
		v.Sign(ed25519.NewKeyFromSeed(make([]byte, 32)))
		w, _ := kv.WrapTransaction(v)
		return w
	}
	one := signed(1)
	if _, err := s.Submit(ctx, one); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(ctx, signed(2)); !errors.Is(err, core.ErrFull) {
		t.Fatal("capacity", err)
	}
	if _, err := s.Submit(ctx, one); err != nil {
		t.Fatal("duplicate at capacity", err)
	}
	native, _ := kv.UnwrapTransaction(one)
	native.Signature[0] ^= 1
	bad, _ := kv.WrapTransaction(native)
	if _, err := s.Submit(ctx, bad); !errors.Is(err, api.ErrInvalidInput) {
		t.Fatal("signature", err)
	}
	small, _ := api.New(api.Config{MaxTransactionBytes: 1}, api.Deps{Protocol: p, Ledger: l, Pool: pool})
	if _, err := small.Submit(ctx, one); !errors.Is(err, api.ErrTooLarge) {
		t.Fatal("limit", err)
	}
	other, _ := kv.New(kv.Config{ChainID: "else"})
	if _, err := api.New(api.Config{}, api.Deps{Protocol: other, Ledger: l, Pool: pool}); err == nil {
		t.Fatal("accepted mismatching protocol")
	}
}

type faultyLedger struct {
	api.Ledger
	failure error
}

func (f faultyLedger) Confirmation(context.Context, core.ID) (query.Confirmation, error) {
	return query.Confirmation{}, f.failure
}

type closingState struct {
	core.StateReader
	failure error
}

func (s closingState) Close() error { return s.failure }

type closingLedger struct {
	api.Ledger
	failure error
}

func (l closingLedger) Snapshot(ctx context.Context, id core.ID) (core.StateReader, error) {
	s, e := l.Ledger.Snapshot(ctx, id)
	return closingState{s, l.failure}, e
}
func TestLocalFailuresAndSnapshotCloseArePreserved(t *testing.T) {
	p := testprotocol.New("faults")
	l, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: p})
	failure := errors.New("disk fault")
	mixed := errors.Join(core.ErrNotFound, failure)
	s, _ := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: faultyLedger{l, mixed}, Pool: pool})
	tx := testprotocol.Tx{Label: "one"}
	if _, err := s.Submit(context.Background(), tx); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := s.Transaction(context.Background(), tx.ID()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := s.Receipt(context.Background(), tx.ID()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	s, _ = api.New(api.Config{}, api.Deps{Protocol: p, Ledger: closingLedger{l, failure}, Pool: pool})
	if _, err := s.Head(context.Background()); !errors.Is(err, failure) {
		t.Fatal("lost close failure", err)
	}
}

type ownerlessLedger struct{ api.Ledger }

func (ownerlessLedger) LedgerID() *core.LedgerIdentity { return nil }

type failingPool struct {
	core.Mempool
	failure error
}

func (p failingPool) Add(context.Context, core.Transaction) error            { return p.failure }
func (p failingPool) Candidates(context.Context) ([]core.Transaction, error) { return nil, p.failure }
func TestAdmissionPreservesPoolFaultsAndRejectsOwnerlessLedger(t *testing.T) {
	p := testprotocol.New("pool-failure")
	l, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: p})
	if _, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ownerlessLedger{l}, Pool: pool}); !errors.Is(err, core.ErrLedgerMismatch) {
		t.Fatal(err)
	}
	failure := errors.Join(core.ErrNotFound, errors.New("pool decoder failure"))
	s, _ := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: l, Pool: failingPool{pool, failure}})
	tx := testprotocol.Tx{Label: "one"}
	if _, err := s.Submit(context.Background(), tx); err != failure || errors.Is(err, api.ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := s.Transaction(context.Background(), tx.ID()); err != failure {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SubmitEncoded(ctx, []byte("not valid")); !errors.Is(err, context.Canceled) || errors.Is(err, api.ErrInvalidInput) {
		t.Fatal(err)
	}
}

type apiProtocol = api.Protocol
type validationFaultProtocol struct {
	apiProtocol
	failure error
	cancel  context.CancelFunc
}

func (p validationFaultProtocol) ValidateTransaction(context.Context, core.Transaction) error {
	if p.cancel != nil {
		p.cancel()
	}
	return p.failure
}

type invalidConfirmationLedger struct {
	api.Ledger
	value query.Confirmation
}

func (l invalidConfirmationLedger) Confirmation(context.Context, core.ID) (query.Confirmation, error) {
	return l.value, nil
}
func TestValidationFaultTreePreserved(t *testing.T) {
	base := testprotocol.New("validation-fault")
	ledger, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: base})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: base})
	raw, _ := base.EncodeTransaction(testprotocol.Tx{Label: "one"})
	fault := errors.New("validator backend failure")
	for _, tc := range []struct {
		name    string
		failure error
		cancel  bool
		invalid bool
	}{
		{"fault", fault, false, false},
		{"fault and cancellation", errors.Join(fault, context.Canceled), true, false},
		{"fault and rejection", errors.Join(fault, core.ErrInvalidTransaction), false, false},
		{"explicit rejection", fmt.Errorf("signature rejected: %w", core.ErrInvalidTransaction), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := validationFaultProtocol{apiProtocol: base, failure: tc.failure}
			if tc.cancel {
				p.cancel = cancel
			}
			s, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ledger, Pool: pool})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.SubmitEncoded(ctx, raw)
			if !errors.Is(err, tc.failure) {
				t.Fatalf("lost validator cause: %v", err)
			}
			if errors.Is(err, api.ErrInvalidInput) != tc.invalid {
				t.Fatalf("wrong invalid classification: %v", err)
			}
		})
	}
}
func TestInvalidConfirmationCannotReportConfirmed(t *testing.T) {
	p := testprotocol.New("invalid-confirmation")
	ledger, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 1, MaxBytes: 10000}, mempool.Deps{Protocol: p})
	tx := testprotocol.Tx{Label: "one"}
	other := testprotocol.Tx{Label: "other"}
	for _, c := range []query.Confirmation{
		{},
		{Transaction: core.ConfirmedTransaction{Transaction: other, BlockID: "block"}, Head: "head"},
		{Transaction: core.ConfirmedTransaction{Transaction: tx}, Head: "head"},
		{Transaction: core.ConfirmedTransaction{Transaction: tx, BlockID: "block"}},
	} {
		s, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: invalidConfirmationLedger{ledger, c}, Pool: pool})
		if err != nil {
			t.Fatal(err)
		}
		if a, err := s.Submit(context.Background(), tx); err == nil {
			t.Fatalf("invalid confirmed admission: %+v", a)
		}
	}
}
