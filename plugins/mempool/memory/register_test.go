package memory

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/pluginkit"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
)

func init() {
	pluginkit.Register("protocol/pool-test", func() (core.Protocol, error) { return testprotocol.New("custom"), nil })
}
func TestPluginKitAssembly(t *testing.T) {
	graph := map[string]any{"protocol": config.PluginUse{Use: "protocol/pool-test"}, "pool": config.PluginUse{Use: "mempool/memory", Config: map[string]any{"maxTransactions": 1, "maxBytes": 4096}, Deps: map[string]any{"protocol": "protocol"}}}
	p, _, err := build.Build[core.Mempool](context.Background(), graph, "pool")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Add(context.Background(), testprotocol.Tx{Label: "a"}); err != nil {
		t.Fatal(err)
	}
	other, _, err := build.Build[core.Mempool](context.Background(), graph, "pool")
	if err != nil {
		t.Fatal(err)
	}
	got, err := other.Select(context.Background(), 4096)
	if err != nil || len(got) != 0 {
		t.Fatal("instances share state", err)
	}
}
