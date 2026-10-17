package consensus

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/core"
	"testing"
	"time"
)

type transientHost struct {
	failure            error
	proposals, commits int
	cancel             context.CancelFunc
}

func (h *transientHost) Propose(context.Context) (core.Block, error) {
	h.proposals++
	if h.proposals == 1 {
		return nil, h.failure
	}
	return testBlock{}, nil
}
func (h *transientHost) Validate(context.Context, core.Block) error { return nil }
func (h *transientHost) Commit(context.Context, core.Block) error {
	h.commits++
	h.cancel()
	return nil
}
func TestTransientProposalDoesNotStop(t *testing.T) {
	for _, failure := range []error{core.ErrNoProposal, core.ErrConflict} {
		t.Run(failure.Error(), func(t *testing.T) {
			c, err := NewSingle(SingleConfig{Interval: "1ms"})
			if err != nil {
				t.Fatal(err)
			}
			deadline, timeout := context.WithTimeout(context.Background(), time.Second)
			defer timeout()
			ctx, cancel := context.WithCancel(deadline)
			defer cancel()
			h := &transientHost{failure: failure, cancel: cancel}
			err = c.Run(ctx, h)
			if !errors.Is(err, context.Canceled) || h.commits != 1 {
				t.Fatalf("transient failure stopped production: %v, commits=%d", err, h.commits)
			}
		})
	}
}
