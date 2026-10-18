package identity

import (
	identity "github.com/lengzhao/GoChainKit/identity/libp2p"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("identity/libp2p", identity.New) }
