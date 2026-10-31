package message

import (
	"github.com/lengzhao/GoChainKit/message/router"
	"github.com/lengzhao/pluginkit"
)

func init() { pluginkit.Register("message/router", router.New) }
