// Package backend registers transactional backend factories.
package backend

import (
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/storage/backend"
	"github.com/lengzhao/pluginkit"
)

func init() {
	pluginkit.Register("backend/memory", NewMemory)
	pluginkit.Register("backend/bolt", NewBolt)
}

type Deps struct{}
type MemoryConfig struct{}

func NewMemory(_ MemoryConfig, _ Deps) (capstorage.Backend, error)     { return backend.NewMemory(), nil }
func NewBolt(c backend.BoltConfig, _ Deps) (capstorage.Backend, error) { return backend.NewBolt(c) }
