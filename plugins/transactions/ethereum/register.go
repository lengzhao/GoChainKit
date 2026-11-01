// Package ethereum registers only transactions/ethereum.
package ethereum

import (
	implementation "github.com/lengzhao/GoChainKit/adapter/ethereum"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("transactions/ethereum", implementation.New) }
