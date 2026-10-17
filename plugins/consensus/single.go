// Package single implements development-only single-node block production.
// It provides no distributed agreement or Byzantine fault tolerance.
package consensus

import (
	"context"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"time"
)

type SingleConfig struct {
	Interval string `json:"interval"`
}
type consensus struct{ interval time.Duration }

func init() { pluginkit.Register("consensus/single", NewSingle) }
func NewSingle(cfg SingleConfig) (core.Consensus, error) {
	d, err := time.ParseDuration(cfg.Interval)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("consensus/single: positive interval required")
	}
	return &consensus{interval: d}, nil
}

// Run produces on each tick, serially validating and committing before the next
// proposal. Host callbacks must honor cancellation. Temporary proposal/head
// conflicts retry; fatal host errors terminate Run.
func (c *consensus) Run(ctx context.Context, h core.ConsensusHost) error {
	if h == nil {
		return fmt.Errorf("consensus/single: host required")
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			b, err := h.Propose(ctx)
			if err != nil {
				if errors.Is(err, core.ErrNoProposal) || errors.Is(err, core.ErrConflict) {
					continue
				}
				return fmt.Errorf("propose: %w", err)
			}
			if err := h.Validate(ctx, b); err != nil {
				if errors.Is(err, core.ErrNoProposal) || errors.Is(err, core.ErrConflict) {
					continue
				}
				return fmt.Errorf("validate: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := h.Commit(ctx, b); err != nil {
				var committed *core.CommittedError
				if !errors.As(err, &committed) && (errors.Is(err, core.ErrNoProposal) || errors.Is(err, core.ErrConflict)) {
					continue
				}
				return fmt.Errorf("commit: %w", err)
			}
		}
	}
}
