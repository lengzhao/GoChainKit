// Package coordinator adapts the node coordinator to PluginKit construction.
package coordinator

import (
	"github.com/lengzhao/GoChainKit/core/contracts"
	"github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("node/coordinator", New) }
func New(_ struct{}, deps node.CoordinatorDeps) (contracts.ConsensusHost, error) {
	return node.NewCoordinator(deps)
}
