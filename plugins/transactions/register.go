// Package transactions retains the combined registration entry point.
package transactions

import (
	_ "github.com/lengzhao/GoChainKit/plugins/service/transactions"
	_ "github.com/lengzhao/GoChainKit/plugins/transactions/bitcoin"
	_ "github.com/lengzhao/GoChainKit/plugins/transactions/ethereum"
)
