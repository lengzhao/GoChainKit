package node

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"reflect"
	"testing"
	"time"
)

type testResource struct {
	events *[]string
	name   string
	fail   bool
}

func (r *testResource) Start(context.Context) error {
	*r.events = append(*r.events, "start "+r.name)
	if r.fail {
		return errors.New("start failed")
	}
	return nil
}
func (r *testResource) Stop(context.Context) error {
	*r.events = append(*r.events, "stop "+r.name)
	return nil
}
func TestRunnerRollbackAndDedup(t *testing.T) {
	events := []string{}
	a := &testResource{&events, "a", false}
	b := &testResource{&events, "b", true}
	r := &runner{shutdown: time.Second, resources: []Lifecycle{a, a, b}}
	if err := r.Run(context.Background()); err == nil {
		t.Fatal("startup failure lost")
	}
	want := []string{"start a", "start b", "stop a"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("%v", events)
	}
}
func TestRunnerSupervisesAndStops(t *testing.T) {
	events := []string{}
	a := &testResource{&events, "a", false}
	fatal := errors.New("worker failed")
	r := &runner{shutdown: time.Second, resources: []Lifecycle{a}, workers: []Worker{workerFunc(func(context.Context) error { return fatal }), workerFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })}}
	if err := r.Run(context.Background()); !errors.Is(err, fatal) {
		t.Fatalf("%v", err)
	}
	if !reflect.DeepEqual(events, []string{"start a", "stop a"}) {
		t.Fatal(events)
	}
	if r.Run(context.Background()) == nil {
		t.Fatal("run twice accepted")
	}
}

type workerFunc func(context.Context) error

func (f workerFunc) Run(ctx context.Context) error { return f(ctx) }

func TestRunnerPreservesShutdownFailures(t *testing.T) {
	diskFailure := errors.New("disk failure")
	for _, failure := range []error{errors.Join(context.Canceled, diskFailure), &core.CommittedError{BlockID: "committed", Cause: context.Canceled}} {
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		r := &runner{shutdown: time.Second, workers: []Worker{workerFunc(func(ctx context.Context) error { close(entered); <-ctx.Done(); return failure })}}
		result := make(chan error, 1)
		go func() { result <- r.Run(ctx) }()
		<-entered
		cancel()
		if err := <-result; !errors.Is(err, failure) {
			t.Fatalf("shutdown failure lost: %v", err)
		}
	}
}

type boundResource struct {
	testResource
	owner *core.LedgerIdentity
}

func (r *boundResource) LedgerID() *core.LedgerIdentity { return r.owner }
func (r *boundResource) Run(ctx context.Context) error  { return nil }

type boundLedger struct {
	core.Ledger
	owner *core.LedgerIdentity
}

func (l boundLedger) LedgerID() *core.LedgerIdentity { return l.owner }

type boundHost struct {
	core.ConsensusHost
	owner *core.LedgerIdentity
}

func (h boundHost) LedgerID() *core.LedgerIdentity { return h.owner }

type noConsensus struct{}

func (noConsensus) Run(context.Context, core.ConsensusHost) error { return nil }
func TestRejectForeignResourcesAndWorkers(t *testing.T) {
	owner := core.NewLedgerIdentity()
	foreign := &boundResource{owner: core.NewLedgerIdentity()}
	for _, worker := range []bool{false, true} {
		d := RunnerDeps{Ledger: boundLedger{owner: owner}, Host: boundHost{owner: owner}, Consensus: noConsensus{}}
		if worker {
			d.Workers = []Worker{foreign}
		} else {
			d.Resources = []core.Lifecycle{foreign}
		}
		if _, e := NewRunner(RunnerConfig{}, d); !errors.Is(e, core.ErrLedgerMismatch) {
			t.Fatal(e)
		}
	}
}

type resourceLedger struct {
	boundLedger
	testResource
}

func TestExplicitLedgerResourceOrder(t *testing.T) {
	events := []string{}
	owner := core.NewLedgerIdentity()
	ledger := &resourceLedger{boundLedger: boundLedger{owner: owner}, testResource: testResource{events: &events, name: "ledger"}}
	archive := &testResource{events: &events, name: "archive"}
	n, e := NewRunner(RunnerConfig{}, RunnerDeps{Ledger: ledger, Host: boundHost{owner: owner}, Consensus: noConsensus{}, Resources: []Lifecycle{archive, ledger}})
	if e != nil {
		t.Fatal(e)
	}
	_ = n.Run(context.Background())
	want := []string{"start archive", "start ledger", "stop ledger", "stop archive"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("%v", events)
	}
}

func TestJoinedShutdownCancellationIsClean(t *testing.T) {
	for _, e := range []error{errors.Join(context.Canceled), errors.Join(context.Canceled, context.DeadlineExceeded)} {
		if !pureCancellation(e) {
			t.Fatalf("pure joined shutdown misclassified: %v", e)
		}
	}
	if pureCancellation(errors.Join(context.Canceled, errors.New("disk"))) {
		t.Fatal("mixed fault discarded")
	}
}
