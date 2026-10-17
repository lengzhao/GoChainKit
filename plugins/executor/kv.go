// Package executor adapts domain execution implementations to PluginKit.
package executor

import (
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit"
)

type KVConfig struct{}
type Deps struct {
	Protocol core.ExecutionProtocol `json:"protocol"`
}

func NewKV(_ KVConfig, d Deps) (core.Executor, error) { return kv.NewExecutor(d.Protocol) }

// ValueKey preserves the existing public helper while KV owns its semantics.
func ValueKey(address kv.Address, key []byte) []byte { return kv.ValueKey(address, key) }
func init()                                          { pluginkit.Register("executor/kv", NewKV) }
