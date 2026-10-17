// Package kv registers the default KV protocol.
package kv

import (
	"github.com/lengzhao/GoChainKit/core/contracts"
	protocol "github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit"
)

type Config = protocol.Config

func init()                                    { pluginkit.Register("protocol/kv", New) }
func New(c Config) (contracts.Protocol, error) { return protocol.New(c) }
