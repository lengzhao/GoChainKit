// Package query registers the transport-independent API service.
package query

import (
	"github.com/lengzhao/GoChainKit/api"
	"github.com/lengzhao/pluginkit"
)

type Config = api.Config
type Deps = api.Deps

func New(c Config, d Deps) (*api.Service, error) { return api.New(c, d) }
func init()                                      { pluginkit.Register("api/query", New) }
