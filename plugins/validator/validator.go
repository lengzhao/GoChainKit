package validator

import (
	"github.com/lengzhao/GoChainKit/validator/static"
	"github.com/lengzhao/pluginkit"
)

func init()                                                           { pluginkit.Register("validator/static", NewStatic) }
func NewStatic(c static.Config, _ struct{}) (*static.Provider, error) { return static.New(c) }
