// Package single implements development-only single-node block production.
// It provides no distributed agreement or Byzantine fault tolerance.
package single

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	"time"
)

type Config struct {
	Interval string `json:"interval"`
}
type consensus struct{ interval time.Duration }

func init() { pluginkit.Register("consensus/single", New) }
func New(cfg Config) (core.Consensus, error) {
	d, err := time.ParseDuration(cfg.Interval)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("consensus/single: positive interval required")
	}
	return &consensus{interval: d}, nil
}

// Run produces on each tick, serially validating and committing before the next
// proposal. Host callbacks must honor cancellation. Any host error terminates Run.
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
				return fmt.Errorf("propose: %w", err)
			}
			if err := h.Validate(ctx, b); err != nil {
				return fmt.Errorf("validate: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := h.Commit(ctx, b); err != nil {
				return fmt.Errorf("commit: %w", err)
			}
		}
	}
}
