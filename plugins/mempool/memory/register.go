package memory

import "github.com/lengzhao/pluginkit"

func init() { pluginkit.Register("mempool/memory", New) }
