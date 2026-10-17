package executor

import (
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/pluginkit"
)

type ComposedConfig struct{}
type ComposedDeps struct {
	Protocol core.Protocol   `json:"protocol"`
	History  core.ChainStore `json:"history"`
}

func NewComposed(_ ComposedConfig, d ComposedDeps) (core.Executor, error) {
	return composed.NewExecutor(d.Protocol, d.History)
}
func init() { pluginkit.Register("executor/composed", NewComposed) }
