// Package bitcoin registers only api/native-bitcoin.
package bitcoin

import (
	implementation "github.com/lengzhao/GoChainKit/api/http/bitcoin"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("api/native-bitcoin", implementation.New) }
