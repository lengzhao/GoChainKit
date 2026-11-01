// Package evm registers only model/evm.
package evm

import (
	implementation "github.com/lengzhao/GoChainKit/model/evm"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("model/evm", implementation.New) }
