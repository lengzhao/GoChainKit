// Package all registers the built-in plugins. Custom binaries may import only
// the implementations they need and register their own constructors.
package all

import (
	_ "github.com/lengzhao/GoChainKit/plugins/api/account"
	_ "github.com/lengzhao/GoChainKit/plugins/api/height"
	_ "github.com/lengzhao/GoChainKit/plugins/api/http"
	_ "github.com/lengzhao/GoChainKit/plugins/api/query"
	_ "github.com/lengzhao/GoChainKit/plugins/api/utxo"
	_ "github.com/lengzhao/GoChainKit/plugins/archive"
	_ "github.com/lengzhao/GoChainKit/plugins/authority"
	_ "github.com/lengzhao/GoChainKit/plugins/backend"
	_ "github.com/lengzhao/GoChainKit/plugins/body"
	_ "github.com/lengzhao/GoChainKit/plugins/builder"
	_ "github.com/lengzhao/GoChainKit/plugins/consensus"
	_ "github.com/lengzhao/GoChainKit/plugins/executor"
	_ "github.com/lengzhao/GoChainKit/plugins/identity"
	_ "github.com/lengzhao/GoChainKit/plugins/keystore"
	_ "github.com/lengzhao/GoChainKit/plugins/mempool"
	_ "github.com/lengzhao/GoChainKit/plugins/message"
	_ "github.com/lengzhao/GoChainKit/plugins/network"
	_ "github.com/lengzhao/GoChainKit/plugins/node"
	_ "github.com/lengzhao/GoChainKit/plugins/protocol"
	_ "github.com/lengzhao/GoChainKit/plugins/query"
	_ "github.com/lengzhao/GoChainKit/plugins/replication"
	_ "github.com/lengzhao/GoChainKit/plugins/secret"
	_ "github.com/lengzhao/GoChainKit/plugins/selector"
	_ "github.com/lengzhao/GoChainKit/plugins/signer"
	_ "github.com/lengzhao/GoChainKit/plugins/state/journal"
	_ "github.com/lengzhao/GoChainKit/plugins/storage"
	_ "github.com/lengzhao/GoChainKit/plugins/validator"
)
