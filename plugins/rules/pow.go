// Package rules registers consensus-rule components independently of models.
package rules

import (
	"github.com/lengzhao/GoChainKit/rules/pow"
	"github.com/lengzhao/pluginkit"
)

// PoWDeps reserves a typed assembly boundary without inventing dependencies.
type PoWDeps struct{}

func NewPoW(c pow.Config, _ PoWDeps) (*pow.Rules, error) { return pow.New(c) }

func init() { pluginkit.Register("rules/pow", NewPoW) }
