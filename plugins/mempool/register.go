package mempool

import "github.com/lengzhao/pluginkit"

func init() { pluginkit.Register("mempool/memory", NewMemory) }
