package account

import (
	routes "github.com/lengzhao/GoChainKit/api/http/account"
	"github.com/lengzhao/pluginkit"
)

type Config = routes.Config
type Deps = routes.Deps

func New(c Config, d Deps) (*routes.Routes, error) { return routes.New(c, d) }
func init()                                        { pluginkit.Register("api/account", New) }
