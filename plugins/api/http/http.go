package http

import (
	apihttp "github.com/lengzhao/GoChainKit/api/http"
	"github.com/lengzhao/pluginkit"
)

type Config = apihttp.Config
type Deps = apihttp.Deps

func New(c Config, d Deps) (*apihttp.Server, error) { return apihttp.New(c, d) }
func init()                                         { pluginkit.Register("api/http", New) }
