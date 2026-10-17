package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/lengzhao/GoChainKit/core"
)

type Lifecycle = core.Lifecycle

// Worker must return promptly after cancellation. A nil return before shutdown
// is unexpected and stops the node just like a reported failure.
type Worker interface{ Run(context.Context) error }
type BoundHost interface {
	core.ConsensusHost
	core.LedgerBound
}
type RunnerConfig struct {
	ShutdownTimeout string `json:"shutdownTimeout"`
}
type RunnerDeps struct {
	Ledger    core.Ledger      `json:"ledger"`
	Host      BoundHost        `json:"host"`
	Consensus core.Consensus   `json:"consensus"`
	Resources []core.Lifecycle `json:"resources,omitempty"`
	Workers   []Worker         `json:"workers,omitempty"`
}
type runner struct {
	used      atomic.Bool
	shutdown  time.Duration
	resources []Lifecycle
	workers   []Worker
}
type consensusWorker struct {
	consensus core.Consensus
	host      core.ConsensusHost
}

func (w consensusWorker) Run(ctx context.Context) error { return w.consensus.Run(ctx, w.host) }
func NewRunner(cfg RunnerConfig, d RunnerDeps) (core.Node, error) {
	if d.Ledger == nil || d.Host == nil || d.Consensus == nil {
		return nil, fmt.Errorf("node: ledger, host and consensus required")
	}
	if d.Ledger.LedgerID() == nil || d.Host.LedgerID() != d.Ledger.LedgerID() {
		return nil, core.ErrLedgerMismatch
	}
	timeout := 5 * time.Second
	if cfg.ShutdownTimeout != "" {
		var err error
		timeout, err = time.ParseDuration(cfg.ShutdownTimeout)
		if err != nil || timeout <= 0 {
			return nil, fmt.Errorf("node: positive shutdown timeout required")
		}
	}
	resources := []Lifecycle{}
	if resource, ok := d.Ledger.(Lifecycle); ok {
		resources = append(resources, resource)
	}
	resources = append(resources, d.Resources...)
	for _, r := range resources {
		if r == nil || !reflect.TypeOf(r).Comparable() || isNil(r) {
			return nil, fmt.Errorf("node: resource must have comparable nonnil identity")
		}
	}
	workers := []Worker{consensusWorker{d.Consensus, d.Host}}
	for _, w := range d.Workers {
		if w == nil || isNil(w) {
			return nil, fmt.Errorf("node: nil worker")
		}
		workers = append(workers, w)
	}
	return &runner{shutdown: timeout, resources: resources, workers: workers}, nil
}
func isNil(v any) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return r.IsNil()
	}
	return false
}
func (r *runner) Run(ctx context.Context) (out error) {
	if !r.used.CompareAndSwap(false, true) {
		return fmt.Errorf("node: runtime is single use")
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	started := []Lifecycle{}
	seen := map[Lifecycle]bool{}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), r.shutdown)
		defer cancel()
		for i := len(started) - 1; i >= 0; i-- {
			out = errors.Join(out, started[i].Stop(stopCtx))
		}
	}()
	for _, resource := range r.resources {
		if seen[resource] {
			continue
		}
		seen[resource] = true
		if err := resource.Start(ctx); err != nil {
			return fmt.Errorf("node start: %w", err)
		}
		started = append(started, resource)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(r.workers))
	for _, worker := range r.workers {
		go func(w Worker) { results <- w.Run(runCtx) }(worker)
	}
	remaining := len(r.workers)
	for remaining > 0 {
		select {
		case err := <-results:
			remaining--
			if runCtx.Err() == nil {
				if err == nil {
					err = fmt.Errorf("node: worker exited unexpectedly")
				}
				out = errors.Join(out, err)
				cancel()
			} else if err != nil && !pureCancellation(err) {
				out = errors.Join(out, err)
			}
		case <-ctx.Done():
			cancel()
			ctx = context.WithoutCancel(ctx)
		}
	}
	return out
}

// Preserve mixed failures and typed committed outcomes even during cancellation.
func pureCancellation(err error) bool {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if _, ok := err.(*core.CommittedError); ok {
		return false
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return pureCancellation(wrapped.Unwrap())
	}
	return false
}
