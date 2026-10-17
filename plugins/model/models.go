// Package model registers independent transaction/state engines.
package model

import (
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/pluginkit"
)

func init() {
	pluginkit.Register("model/account", NewAccount)
	pluginkit.Register("model/utxo", NewUTXO)
}

type AccountDeps struct{}
type UTXODeps struct{}

func NewAccount(c account.Config, _ AccountDeps) (model.Engine, error) { return account.New(c) }
func NewUTXO(c utxo.Config, _ UTXODeps) (model.Engine, error)          { return utxo.New(c) }
