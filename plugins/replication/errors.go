package replication

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

func localService(e error) error {
	if e == nil || pureCancellation(e) {
		return e
	}
	return &core.LocalServiceError{Cause: e}
}
func pureCancellation(e error) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *core.CommittedError, *core.TransactionError, *core.LocalServiceError:
		return false
	case interface{ Unwrap() []error }:
		children := v.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !pureCancellation(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return pureCancellation(v.Unwrap())
	}
	return e == context.Canceled || e == context.DeadlineExceeded
}

// Preserve local lookup failures propagated through protocol reference checks.
type supervisedChain struct{ core.ChainStore }

func (s supervisedChain) Head(ctx context.Context) (core.ID, error) {
	id, e := s.ChainStore.Head(ctx)
	if e != nil && !onlyExpected(e, func(e error) bool { return e == core.ErrNotFound }) {
		e = localService(e)
	}
	return id, e
}
func (s supervisedChain) Block(ctx context.Context, id core.ID) (core.Block, error) {
	b, e := s.ChainStore.Block(ctx, id)
	if e != nil && !onlyExpected(e, func(e error) bool { return e == core.ErrNotFound }) {
		e = localService(e)
	}
	return b, e
}
func (s supervisedChain) Receipt(ctx context.Context, id core.ID) (core.Receipt, error) {
	r, e := s.ChainStore.Receipt(ctx, id)
	if e != nil && !onlyExpected(e, func(e error) bool { return e == core.ErrNotFound }) {
		e = localService(e)
	}
	return r, e
}

// Inspect the whole error tree: errors.Is alone would classify a mixed local
// failure plus peer rejection/cancellation as a harmless remote error.
func expectedExecutionError(e error) bool {
	return onlyExpected(e, func(e error) bool {
		if tx, ok := e.(*core.TransactionError); ok {
			return tx.Disposition == core.TransactionRejected || tx.Disposition == core.TransactionDeferred
		}
		return e == core.ErrInvalidTransaction || e == core.ErrDeferredTransaction || e == context.Canceled || e == context.DeadlineExceeded
	})
}
func expectedCommitError(e error) bool {
	return onlyExpected(e, func(e error) bool {
		return e == core.ErrConflict || e == core.ErrAlreadyCommitted || e == context.Canceled || e == context.DeadlineExceeded
	})
}
func onlyExpected(e error, leaf func(error) bool) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *core.CommittedError, *core.LocalServiceError:
		return false
	case *core.TransactionError:
		return leaf(e)
	case interface{ Unwrap() []error }:
		children := v.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyExpected(child, leaf) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return onlyExpected(v.Unwrap(), leaf)
	}
	return leaf(e)
}
