package height

import (
	"github.com/lengzhao/GoChainKit/api/http/height"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("api/height", height.New) }
