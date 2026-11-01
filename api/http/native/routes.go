// Package native preserves the original combined native API import path.
// New binaries can import api/http/bitcoin or api/http/evm independently.
package native

import (
	btc "github.com/lengzhao/GoChainKit/api/http/bitcoin"
	evm "github.com/lengzhao/GoChainKit/api/http/evm"
)

type Config = btc.Config
type Deps = btc.Deps
type BitcoinRoutes = btc.Routes
type EVMRoutes = evm.Routes

func NewBitcoin(c Config, d Deps) (*BitcoinRoutes, error) { return btc.New(c, d) }
func NewEVM(c Config, d Deps) (*EVMRoutes, error)         { return evm.New(evm.Config(c), evm.Deps(d)) }
