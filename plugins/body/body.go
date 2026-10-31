package body

import (
	"github.com/lengzhao/GoChainKit/body/materializer"
	"github.com/lengzhao/GoChainKit/body/remote"
	"github.com/lengzhao/pluginkit"
)

func init() {
	pluginkit.Register("body/local", materializer.New)
	pluginkit.Register("body/fetch", materializer.New)
	pluginkit.Register("body/remote", remote.New)
	pluginkit.Register("body/server", remote.NewServer)
}
