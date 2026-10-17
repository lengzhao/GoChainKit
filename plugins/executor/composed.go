package executor

import (
	"fmt"
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
	base, ok := d.Protocol.(interface{ Base() *composed.Protocol })
	if !ok {
		return nil, fmt.Errorf("executor requires composed protocol capability")
	}
	return composed.NewExecutor(base.Base(), d.History)
}
func init() { pluginkit.Register("executor/composed", NewComposed) }
