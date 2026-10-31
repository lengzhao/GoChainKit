package archive

import (
	"github.com/lengzhao/GoChainKit/archive/transactions"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("archive/transactions", transactions.New) }
