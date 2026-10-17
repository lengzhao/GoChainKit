package protocol

import (
	"github.com/lengzhao/GoChainKit/core"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"github.com/lengzhao/pluginkit"
)

type PoWKVConfig = pow.Config
type PoWKVExecutorConfig struct{}
type PoWKVExecutorDeps struct {
	Protocol core.WorkProtocol `json:"protocol"`
	Inner    core.Executor     `json:"inner"`
}

func init() {
	pluginkit.Register("protocol/powkv", NewPoWKV)
	pluginkit.Register("executor/powkv", NewPoWKVExecutor)
}
func NewPoWKV(c PoWKVConfig) (core.WorkProtocol, error) { return pow.New(c) }
func NewPoWKVExecutor(_ PoWKVExecutorConfig, d PoWKVExecutorDeps) (core.Executor, error) {
	return pow.NewExecutor(d.Protocol, d.Inner)
}
