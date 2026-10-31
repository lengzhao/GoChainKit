package query

import (
	"github.com/lengzhao/GoChainKit/query/height"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("query/height", height.New) }
