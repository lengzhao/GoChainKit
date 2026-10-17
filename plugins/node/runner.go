package node

import (
	"github.com/lengzhao/GoChainKit/core"
	runtime "github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("node/default", NewDefault) }
func NewDefault(c runtime.RunnerConfig, d runtime.RunnerDeps) (core.Node, error) {
	return runtime.NewRunner(c, d)
}
