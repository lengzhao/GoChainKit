// Package journal registers the execution-local journal factory.
package journal

import (
	"github.com/lengzhao/GoChainKit/state/journal"
	"github.com/lengzhao/pluginkit"
)

type Config = journal.Config
type Deps struct{}

func New(c Config, _ Deps) (*journal.Factory, error) { return journal.New(c) }
func init()                                          { pluginkit.Register("state/journal", New) }
