// Package kv registers the default KV protocol.
package protocol

import (
	"github.com/lengzhao/GoChainKit/core"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit"
)

type KVConfig = protocol.Config

func init()                                   { pluginkit.Register("protocol/kv", NewKV) }
func NewKV(c KVConfig) (core.Protocol, error) { return protocol.New(c) }
