// Package selector provides replaceable candidate ordering and budget policies.
package selector

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
)

type FIFOConfig struct{}
type Deps struct {
	Protocol core.TransactionProtocol `json:"protocol"`
}
type fifo struct{ protocol core.TransactionProtocol }

func init() { pluginkit.Register("selector/fifo", NewFIFO) }
func NewFIFO(_ FIFOConfig, d Deps) (core.TransactionSelector, error) {
	if d.Protocol == nil {
		return nil, fmt.Errorf("selector: protocol required")
	}
	if err := d.Protocol.Protocol().Validate(); err != nil {
		return nil, err
	}
	return &fifo{d.Protocol}, nil
}
func (f *fifo) Protocol() core.ProtocolDescriptor { return f.protocol.Protocol() }
func (f *fifo) Select(ctx context.Context, candidates []core.Transaction, budget int) ([]core.Transaction, error) {
	if budget < 0 {
		return nil, fmt.Errorf("negative selection budget")
	}
	var out []core.Transaction
	for _, tx := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tx == nil || tx.ID() == "" {
			return nil, core.ErrInvalidTransaction
		}
		data, err := f.protocol.EncodeTransaction(tx)
		if err != nil {
			return nil, err
		}
		if len(data) > budget {
			continue
		}
		copy, err := f.protocol.DecodeTransaction(data)
		if err != nil {
			return nil, err
		}
		if copy == nil || copy.ID() != tx.ID() {
			return nil, fmt.Errorf("selector codec changed identity")
		}
		out = append(out, copy)
		budget -= len(data)
	}
	return out, nil
}
