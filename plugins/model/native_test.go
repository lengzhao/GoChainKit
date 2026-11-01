package model_test

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/model"
	_ "github.com/lengzhao/GoChainKit/plugins/model"
	"github.com/lengzhao/pluginkit/build"
	"os/exec"
	"strings"
	"testing"
)

// The narrow plugin imports are usable without linking an unrelated EVM stack.
func TestBitcoinNarrowDependencies(t *testing.T) {
	for _, pkg := range []string{"plugins/model/bitcoin", "plugins/api/bitcoin", "plugins/transactions/bitcoin"} {
		out, err := exec.Command("go", "list", "-deps", "github.com/lengzhao/GoChainKit/"+pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", pkg, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "github.com/ethereum/") || strings.HasPrefix(line, "github.com/lengzhao/GoChainKit/model/evm") {
				t.Fatalf("%s links unrelated dependency %s", pkg, line)
			}
		}
	}
}

func TestNativeModelRegistration(t *testing.T) {
	for _, tc := range []struct {
		kind string
		cfg  map[string]any
	}{
		{"model/bitcoin", map[string]any{"network": "btc", "genesisTime": int64(1700000000), "allocations": []any{map[string]any{"value": "1000", "script": "UQ=="}}}},
		{"model/evm", map[string]any{"network": "evm", "chainID": "1337", "allocations": map[string]any{"0x0000000000000000000000000000000000000001": map[string]any{"balance": "1000"}}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			engine, _, err := build.Build[model.Engine](context.Background(), map[string]any{"engine": map[string]any{"use": tc.kind, "config": tc.cfg}}, "engine")
			if err != nil {
				t.Fatal(err)
			}
			state, err := engine.Genesis(context.Background())
			if err != nil || len(state) == 0 {
				t.Fatalf("genesis %v", err)
			}
		})
	}
}
