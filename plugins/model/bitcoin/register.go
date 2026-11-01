// Package bitcoin registers only model/bitcoin.
package bitcoin

import (
	implementation "github.com/lengzhao/GoChainKit/model/bitcoin"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("model/bitcoin", implementation.New) }
