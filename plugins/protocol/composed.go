package protocol

import (
	"encoding/hex"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/pluginkit"
)

type ComposedConfig struct {
	Network     string `json:"network"`
	GenesisTime int64  `json:"genesisTime"`
	Producer    string `json:"producer"`
}
type ComposedDeps struct {
	Model  model.Engine    `json:"model"`
	Rule   composed.Rule   `json:"rule"`
	Choice composed.Choice `json:"choice"`
}
type ComposedPoWDeps struct {
	Model  model.Engine      `json:"model"`
	Rule   composed.WorkRule `json:"rule"`
	Choice composed.Choice   `json:"choice"`
}

func composedConfig(c ComposedConfig) (composed.Config, error) {
	producer, e := hex.DecodeString(c.Producer)
	if e != nil {
		return composed.Config{}, fmt.Errorf("invalid producer hex: %w", e)
	}
	return composed.Config{Network: c.Network, GenesisTime: c.GenesisTime, Producer: producer}, nil
}
func NewComposed(c ComposedConfig, d ComposedDeps) (core.Protocol, error) {
	cfg, e := composedConfig(c)
	if e != nil {
		return nil, e
	}
	return composed.New(cfg, d.Model, d.Rule, d.Choice)
}
func NewComposedPoW(c ComposedConfig, d ComposedPoWDeps) (core.WorkProtocol, error) {
	cfg, e := composedConfig(c)
	if e != nil {
		return nil, e
	}
	return composed.NewPoW(cfg, d.Model, d.Rule, d.Choice)
}
func init() {
	pluginkit.Register("protocol/composed", NewComposed)
	pluginkit.Register("protocol/composed-pow", NewComposedPoW)
}
