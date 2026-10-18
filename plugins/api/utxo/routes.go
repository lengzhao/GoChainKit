package utxo

import (
	routes "github.com/lengzhao/GoChainKit/api/http/utxo"
	"github.com/lengzhao/pluginkit"
)

type Config = routes.Config
type Deps = routes.Deps

func New(c Config, d Deps) (*routes.Routes, error) { return routes.New(c, d) }
func init()                                        { pluginkit.Register("api/utxo", New) }
