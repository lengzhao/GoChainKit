package assembly

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"github.com/lengzhao/GoChainKit/protocol/kv"
	"github.com/lengzhao/pluginkit/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPresetRunAndReopen(t *testing.T) {
	data, err := os.ReadFile("../presets/single.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.ReplaceAll(string(data), "data/chain.db", filepath.Join(t.TempDir(), "chain.db"))
	cfg = strings.ReplaceAll(cfg, "interval: 1s", "interval: 2ms")
	cfg = strings.ReplaceAll(cfg, "127.0.0.1:8080", "127.0.0.1:0")
	var previous uint64
	for phase := 0; phase < 2; phase++ {
		root, result, err := Load(context.Background(), strings.NewReader(cfg))
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := build.RequireByID[core.Ledger](result, "ledger", "ledger")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() { finished <- root.Run(ctx) }()
		deadline := time.NewTimer(5 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		advanced := false
		for !advanced {
			select {
			case err := <-finished:
				cancel()
				t.Fatalf("runtime stopped early: %v", err)
			case <-deadline.C:
				cancel()
				<-finished
				t.Fatal("head did not advance")
			case <-tick.C:
				head, e := ledger.Head(context.Background())
				if e != nil {
					continue
				}
				block, e := ledger.Block(context.Background(), head)
				if e != nil {
					continue
				}
				raw, e := kv.UnwrapBlock(block)
				advanced = e == nil && raw.Header.Height > previous
			}
		}
		tick.Stop()
		deadline.Stop()
		cancel()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		_, again, err := Load(context.Background(), strings.NewReader(cfg))
		if err != nil {
			t.Fatal(err)
		}
		lifecycle, err := build.RequireByID[core.Lifecycle](again, "ledger", "ledger")
		if err != nil {
			t.Fatal(err)
		}
		if err := lifecycle.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		reopened, _ := build.RequireByID[core.Ledger](again, "ledger", "ledger")
		head, err := reopened.Head(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		block, err := reopened.Block(context.Background(), head)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := kv.UnwrapBlock(block)
		if err != nil || raw.Header.Height <= previous {
			t.Fatalf("head did not advance: %v", err)
		}
		previous = raw.Header.Height
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInvalidEnvelope(t *testing.T) {
	for _, s := range []string{"root: node\nunknown: true\nplugins: {}", "root: node\nplugins: {}", "root: node\nplugins: {}\n---\nroot: other", strings.Repeat("x", MaxConfigBytes+1)} {
		if _, _, err := Load(context.Background(), strings.NewReader(s)); err == nil {
			t.Fatal("bad configuration accepted")
		}
	}
}
