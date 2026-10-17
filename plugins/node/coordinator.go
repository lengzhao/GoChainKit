// Package coordinator adapts the node coordinator to PluginKit construction.
package node

import (
	"github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("node/coordinator", NewCoordinator) }
func NewCoordinator(_ struct{}, deps node.CoordinatorDeps) (node.BoundHost, error) {
	return node.NewCoordinator(deps)
}
