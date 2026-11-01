// Package transactions registers only service/transactions.
package transactions

import (
	implementation "github.com/lengzhao/GoChainKit/service/transactions"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("service/transactions", implementation.New) }
