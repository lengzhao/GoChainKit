package composed_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	pow "github.com/lengzhao/GoChainKit/rules/pow"
	"reflect"
	"testing"
	"time"
)

type counterModel struct{ *testprotocol.Format }

func (m counterModel) Genesis(context.Context) (map[string][]byte, error) {
	return map[string][]byte{"counter": make([]byte, 8)}, nil
}
func (m counterModel) Execute(ctx context.Context, s core.StateReader, txs []core.Transaction) (model.Transition, error) {
	v, e := s.Get(ctx, []byte("counter"))
	if e != nil {
		return model.Transition{}, e
	}
	return model.Transition{Writes: map[string][]byte{"counter": binary.BigEndian.AppendUint64(nil, binary.BigEndian.Uint64(v)+uint64(len(txs)))}}, nil
}

type contextualCounter struct{ counterModel }

type failingContextualModel struct {
	counterModel
	err error
}

func (m failingContextualModel) ExecuteInContext(context.Context, core.StateReader, []core.Transaction, model.ExecutionContext) (model.Transition, error) {
	return model.Transition{Writes: map[string][]byte{"must-not-commit": {1}}}, m.err
}

func TestContextualExecutionErrorsAreNeverConvertedToSuccess(t *testing.T) {
	for _, failure := range []error{errors.New("model storage fault"), context.Canceled,
		&core.TransactionError{TransactionID: "tx", Disposition: core.TransactionDeferred, Cause: errors.New("not yet executable")},
		&core.TransactionError{TransactionID: "tx", Disposition: core.TransactionRejected, Cause: errors.New("invalid input")}} {
		t.Run(failure.Error(), func(t *testing.T) {
			ctx := context.Background()
			engine := failingContextualModel{counterModel{testprotocol.New("error-propagation")}, failure}
			rule, _ := pow.New(pow.Config{})
			p, err := composed.NewPoW(composed.Config{Network: "error-propagation", GenesisTime: 1700000000}, engine, rule, rule)
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
			if err != nil {
				t.Fatal(err)
			}
			executor, err := composed.NewExecutor(p, ledger)
			if err != nil {
				t.Fatal(err)
			}
			head, _ := ledger.Head(ctx)
			parent, _ := ledger.Block(ctx, head)
			draft, err := p.Draft(ctx, parent, nil)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, _ := ledger.Snapshot(ctx, head)
			defer snapshot.Close()
			result, err := executor.Execute(ctx, snapshot, draft)
			if !errors.Is(err, failure) || len(result.Writes) != 0 || result.BlockID != "" {
				t.Fatalf("model failure became successful execution: result=%+v err=%v", result, err)
			}
		})
	}
}

func (m contextualCounter) ExecuteInContext(ctx context.Context, s core.StateReader, txs []core.Transaction, env model.ExecutionContext) (model.Transition, error) {
	if env.Parent() == nil || env.Candidate() == nil || env.History() == nil {
		panic("missing context")
	}
	if _, unsafe := env.History().(core.Ledger); unsafe {
		panic("mutable history leaked")
	}
	if env.(model.HeightContext).Height() == 0 || env.(model.TimestampContext).Timestamp() == 0 {
		panic("missing candidate coordinates")
	}
	if _, err := env.(model.StateCommitmentContext).StateCommitment(ctx, map[string][]byte{}); err != nil {
		return model.Transition{}, err
	}
	return m.Execute(ctx, s, txs)
}
func TestNonCurrencyModelAndOptionalContext(t *testing.T) {
	ctx := context.Background()
	m := contextualCounter{counterModel{testprotocol.New("counter")}}
	r, _ := pow.New(pow.Config{})
	clock := func() time.Time { return time.Unix(1000000010, 0) }
	p, e := composed.NewPoW(composed.Config{Network: "counter", GenesisTime: 1000000000, Clock: clock}, m, r, r)
	if e != nil {
		t.Fatal(e)
	}
	s, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	executor, e := composed.NewExecutor(p, s)
	if e != nil {
		t.Fatal(e)
	}
	head, _ := s.Head(ctx)
	parent, _ := s.Block(ctx, head)
	draft, e := p.Draft(ctx, parent, []core.Transaction{testprotocol.Tx{Label: "increment"}})
	if e != nil {
		t.Fatal(e)
	}
	snap, _ := s.Snapshot(ctx, head)
	result, e := executor.Execute(ctx, snap, draft)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.Finalize(ctx, draft, result)
	if e != nil {
		t.Fatal(e)
	}
	b, e = p.Seal(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	result.BlockID = b.ID()
	if _, e = s.Import(ctx, head, b, result); e != nil {
		t.Fatal(e)
	}
	snap, _ = s.Snapshot(ctx, b.ID())
	defer snap.Close()
	value, e := snap.Get(ctx, []byte("counter"))
	if e != nil || binary.BigEndian.Uint64(value) != 1 {
		t.Fatal("generic counter model failed", e)
	}
	generic, e := composed.New(composed.Config{Network: "counter", GenesisTime: 1000000000}, m, r, r)
	if e != nil {
		t.Fatal(e)
	}
	if _, fake := any(generic).(consensuscap.WorkProtocol); fake {
		t.Fatal("base protocol falsely advertises work capability")
	}
	envelope, _ := composed.Unwrap(b)
	envelope.Proof = []byte("different certificate")
	other, _ := composed.Wrap(envelope)
	if other.ID() != b.ID() {
		t.Fatal("auxiliary proof changed proposal identity")
	}
	rewardRule, _ := pow.New(pow.Config{InitialReward: 1})
	if _, e = composed.NewPoW(composed.Config{Network: "counter", GenesisTime: 1000000000}, m, rewardRule, rewardRule); e == nil {
		t.Fatal("currency rule accepted non-currency model")
	}
}

type identityCounter struct{ counterModel }

func (m identityCounter) ExecuteInContext(ctx context.Context, s core.StateReader, txs []core.Transaction, env model.ExecutionContext) (model.Transition, error) {
	change, e := m.Execute(ctx, s, txs)
	if e != nil {
		return model.Transition{}, e
	}
	change.Writes["candidate-id"] = []byte(env.Candidate().ID())
	header, e := composed.Unwrap(env.Candidate())
	if e != nil {
		return model.Transition{}, e
	}
	data, e := json.Marshal(header.Header)
	if e != nil {
		return model.Transition{}, e
	}
	change.Writes["candidate-header"] = data
	head, e := env.History().Head(ctx)
	if e != nil {
		return model.Transition{}, e
	}
	change.Writes["execution-head"] = []byte(head)
	return change, nil
}
func TestExecutionCandidateStableBeforeAfterSeal(t *testing.T) {
	ctx := context.Background()
	m := identityCounter{counterModel{testprotocol.New("stable")}}
	rule, _ := pow.New(pow.Config{})
	p, e := composed.NewPoW(composed.Config{Network: "stable", GenesisTime: 1700000000}, m, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	executor, _ := composed.NewExecutor(p, ledger)
	g, _ := ledger.Head(ctx)
	parent, _ := ledger.Block(ctx, g)
	draft, e := p.Draft(ctx, parent, nil)
	if e != nil {
		t.Fatal(e)
	}
	snap, _ := ledger.Snapshot(ctx, g)
	first, e := executor.Execute(ctx, snap, draft)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.Finalize(ctx, draft, first)
	if e != nil {
		t.Fatal(e)
	}
	b, e = p.Seal(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	snap, _ = ledger.Snapshot(ctx, g)
	replayed, e := executor.Execute(ctx, snap, b)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	if first.StateRoot != replayed.StateRoot || !reflect.DeepEqual(first.Writes, replayed.Writes) {
		t.Fatal("execution context changed with finalization or sealing")
	}
}

type historyProbe struct {
	counterModel
	target *core.ID
}

func (m historyProbe) ExecuteInContext(ctx context.Context, s core.StateReader, txs []core.Transaction, env model.ExecutionContext) (model.Transition, error) {
	change, e := m.Execute(ctx, s, txs)
	if e != nil {
		return model.Transition{}, e
	}
	_, e = env.History().Block(ctx, *m.target)
	if e == nil {
		change.Writes["probe"] = []byte("seen")
	} else if errors.Is(e, core.ErrNotFound) {
		change.Writes["probe"] = []byte("absent")
	} else {
		return model.Transition{}, e
	}
	return change, nil
}
func TestExecutionHistoryExcludesLocalSideBranches(t *testing.T) {
	ctx := context.Background()
	var target core.ID
	m := historyProbe{counterModel{testprotocol.New("history")}, &target}
	rule, _ := pow.New(pow.Config{})
	clock := func() time.Time { return time.Unix(1700000010, 0) }
	p, e := composed.NewPoW(composed.Config{Network: "history", GenesisTime: 1700000000, Clock: clock}, m, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	left, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	right, _ := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	leftExec, _ := composed.NewExecutor(p, left)
	rightExec, _ := composed.NewExecutor(p, right)
	g, _ := left.Head(ctx)
	parent, _ := left.Block(ctx, g)
	sideDraft, _ := p.Draft(ctx, parent, []core.Transaction{testprotocol.Tx{Label: "side"}})
	snap, _ := left.Snapshot(ctx, g)
	result, e := leftExec.Execute(ctx, snap, sideDraft)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	side, _ := p.Finalize(ctx, sideDraft, result)
	side, e = p.Seal(ctx, side)
	if e != nil {
		t.Fatal(e)
	}
	result.BlockID = side.ID()
	if _, e = left.Import(ctx, g, side, result); e != nil {
		t.Fatal(e)
	}
	target = side.ID()
	main, _ := p.Draft(ctx, parent, []core.Transaction{testprotocol.Tx{Label: "main"}})
	snap, _ = left.Snapshot(ctx, g)
	a, e := leftExec.Execute(ctx, snap, main)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	snap, _ = right.Snapshot(ctx, g)
	b, e := rightExec.Execute(ctx, snap, main)
	snap.Close()
	if e != nil {
		t.Fatal(e)
	}
	if a.StateRoot != b.StateRoot || !reflect.DeepEqual(a.Writes, b.Writes) {
		t.Fatal("local side branch leaked into execution history")
	}
}
