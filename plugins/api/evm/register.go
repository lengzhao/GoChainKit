// Package evm registers only api/evm.
package evm

import (
	implementation "github.com/lengzhao/GoChainKit/api/http/evm"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("api/evm", implementation.New) }
