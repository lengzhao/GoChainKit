// Package all registers the built-in plugins. Custom binaries may import only
// the implementations they need and register their own constructors.
package all

import (
	_ "github.com/lengzhao/GoChainKit/plugins/builder"
	_ "github.com/lengzhao/GoChainKit/plugins/consensus"
	_ "github.com/lengzhao/GoChainKit/plugins/executor"
	_ "github.com/lengzhao/GoChainKit/plugins/mempool"
	_ "github.com/lengzhao/GoChainKit/plugins/node"
	_ "github.com/lengzhao/GoChainKit/plugins/protocol"
	_ "github.com/lengzhao/GoChainKit/plugins/selector"
	_ "github.com/lengzhao/GoChainKit/plugins/storage"
)
