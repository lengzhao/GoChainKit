package powkv

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	executor "github.com/lengzhao/GoChainKit/plugins/executor"
	kv "github.com/lengzhao/GoChainKit/protocol/kv"
)

type adaptedExecutor struct {
	protocol core.WorkProtocol
	inner    core.Executor
}

func NewExecutor(p core.WorkProtocol) (core.Executor, error) {
	if p == nil || p.Protocol().Name != "powkv" || p.Protocol().Version != "1" {
		return nil, fmt.Errorf("incompatible protocol")
	}
	base, e := kv.New(kv.Config{ChainID: p.Protocol().Network})
	if e != nil {
		return nil, e
	}
	inner, e := executor.NewKV(executor.KVConfig{}, executor.Deps{Protocol: base})
	if e != nil {
		return nil, e
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
