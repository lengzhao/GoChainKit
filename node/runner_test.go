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
