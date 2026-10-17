package protocol

import (
	consensuscap "github.com/lengzhao/GoChainKit/cap/consensus"
	"github.com/lengzhao/GoChainKit/core"
	pow "github.com/lengzhao/GoChainKit/protocol/powkv"
	"github.com/lengzhao/pluginkit"
)

type PoWKVConfig = pow.Config
type PoWKVExecutorConfig struct{}
type PoWKVExecutorDeps struct {
	Protocol consensuscap.WorkProtocol `json:"protocol"`
	Inner    core.Executor             `json:"inner"`
}

func init() {
	pluginkit.Register("protocol/powkv", NewPoWKV)
	pluginkit.Register("executor/powkv", NewPoWKVExecutor)
}
func NewPoWKV(c PoWKVConfig) (consensuscap.WorkProtocol, error) { return pow.New(c) }
func NewPoWKVExecutor(_ PoWKVExecutorConfig, d PoWKVExecutorDeps) (core.Executor, error) {
	return pow.NewExecutor(d.Protocol, d.Inner)
}
