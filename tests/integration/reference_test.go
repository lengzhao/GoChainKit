package integration_test

import (
	"context"
	"github.com/lengzhao/GoChainKit/assembly"
	"github.com/lengzhao/GoChainKit/core"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"github.com/lengzhao/pluginkit/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReferencePresetProductionAndReopen(t *testing.T) {
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "reference-test-password")
	raw, e := os.ReadFile("../../presets/pow-reference.yaml")
	if e != nil {
		t.Fatal(e)
	}
	cfg := strings.ReplaceAll(string(raw), "data/", filepath.ToSlash(t.TempDir())+"/")
	cfg = strings.ReplaceAll(cfg, "/tcp/19301", "/tcp/0")
	cfg = strings.ReplaceAll(cfg, "127.0.0.1:8084", "127.0.0.1:0")
	cfg = strings.ReplaceAll(cfg, "interval: 1s", "interval: 20ms")
	var previous core.ID
	for run := 0; run < 2; run++ {
		node, result, e := assembly.Load(context.Background(), strings.NewReader(cfg))
		if e != nil {
			t.Fatal(e)
		}
		ledger, e := build.RequireByID[core.Ledger](result, "ledger", "ledger")
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- node.Run(ctx) }()
		deadline := time.After(5 * time.Second)
		tick := time.NewTicker(10 * time.Millisecond)
		ready := false
		for !ready {
			select {
			case e := <-done:
				cancel()
				tick.Stop()
				t.Fatalf("node exited: %v", e)
			case <-deadline:
				cancel()
				<-done
				tick.Stop()
				t.Fatal("no production")
			case <-tick.C:
				h, e := ledger.Head(ctx)
				if e == nil {
					b, e := ledger.Block(ctx, h)
					if e == nil {
						p, e := build.RequireByID[interface {
							BlockHeight(core.Block) (uint64, error)
						}](result, "protocol", "protocol")
						if e != nil {
							t.Fatal(e)
						}
						height, e := p.BlockHeight(b)
						ready = e == nil && height > 0 && h != previous
						if ready {
							previous = h
						}
					}
				}
			}
		}
		tick.Stop()
		cancel()
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
}
