// Package bitcoin registers only transactions/bitcoin.
package bitcoin

import (
	implementation "github.com/lengzhao/GoChainKit/adapter/bitcoin"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("transactions/bitcoin", implementation.New) }
