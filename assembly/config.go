// Package assembly loads the application envelope and delegates the complete
// dependency graph to PluginKit; it owns no parallel plugin registry.
package assembly

import (
	"bytes"
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit/build"
	"gopkg.in/yaml.v3"
	"io"
)

const MaxConfigBytes = 1 << 20

type Config struct {
	Root    string         `yaml:"root"`
	Plugins map[string]any `yaml:"plugins"`
}

func Load(ctx context.Context, reader io.Reader) (core.Node, *build.Result, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxConfigBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > MaxConfigBytes {
		return nil, nil, fmt.Errorf("configuration exceeds size limit")
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, fmt.Errorf("expected a single configuration document")
	}
	if cfg.Root == "" || len(cfg.Plugins) == 0 {
		return nil, nil, fmt.Errorf("root and plugins required")
	}
	return build.Build[core.Node](ctx, cfg.Plugins, cfg.Root)
}
