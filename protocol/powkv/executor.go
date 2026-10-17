package powkv

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
)

type adaptedExecutor struct {
	protocol core.WorkProtocol
	inner    core.Executor
}

// NewExecutor adapts an explicitly supplied KV executor to PoW block envelopes.
func NewExecutor(p core.WorkProtocol, inner core.Executor) (core.Executor, error) {
	if p == nil || p.Protocol().Name != "powkv" || p.Protocol().Version != "1" {
		return nil, fmt.Errorf("incompatible protocol")
	}
	expected := core.ProtocolDescriptor{Name: "kv", Version: "1", Network: p.Protocol().Network}
	if err := core.RequireCompatible(expected, inner); err != nil {
		return nil, err
	}
	return &adaptedExecutor{p, inner}, nil
}
func (e *adaptedExecutor) Protocol() core.ProtocolDescriptor { return e.protocol.Protocol() }
func (e *adaptedExecutor) Execute(ctx context.Context, s core.StateReader, b core.Block) (core.ExecutionResult, error) {
	v, err := UnwrapBlock(b)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	converted, err := kv.WrapBlock(kv.Block{Header: v.Header, Transactions: v.Transactions})
	if err != nil {
		return core.ExecutionResult{}, err
	}
	r, err := e.inner.Execute(ctx, s, converted)
	if err != nil {
		return core.ExecutionResult{}, err
	}
	r.BlockID = b.ID()
	return r, nil
}
