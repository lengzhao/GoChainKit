package consensus

import (
	"context"
	replicationcap "github.com/lengzhao/GoChainKit/cap/replication"
	"github.com/lengzhao/GoChainKit/core"
)

func transientProduction(e error) bool {
	if e == nil {
		return true
	}
	switch v := e.(type) {
	case *core.CommittedError, *replicationcap.LocalServiceError:
		return false
	case interface{ Unwrap() []error }:
		for _, child := range v.Unwrap() {
			if !transientProduction(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return transientProduction(v.Unwrap())
	}
	return e == context.Canceled || e == core.ErrConflict || e == core.ErrNoProposal || e == core.ErrAlreadyCommitted
}

func pureCancellation(e error) bool {
	if e == nil {
		return false
	}
	switch v := e.(type) {
	case *core.CommittedError, *core.TransactionError, *replicationcap.LocalServiceError:
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
