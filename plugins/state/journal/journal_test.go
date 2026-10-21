package journal

import (
	"context"
	"sync"
	"testing"

	capstate "github.com/lengzhao/GoChainKit/cap/state"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
)

type empty struct{}

func (empty) Get(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func TestPluginKitFactoryAndInstanceIsolation(t *testing.T) {
	ctx := context.Background()
	graph := map[string]any{"factory": config.PluginUse{Use: "state/journal", Config: map[string]any{"maxEntries": 4, "maxBytes": 64, "maxCheckpointDepth": 2}}}
	f, _, e := build.Build[capstate.Factory](ctx, graph, "factory")
	if e != nil {
		t.Fatal(e)
	}
	other, _, e := build.Build[capstate.Factory](ctx, graph, "factory")
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.New(ctx, empty{})
	if e != nil {
		t.Fatal(e)
	}
	b, e := other.New(ctx, empty{})
	if e != nil {
		t.Fatal(e)
	}
	_ = a.Set(ctx, "a", []byte("first"))
	if _, ok, e := b.Get(ctx, "a"); e != nil || ok {
		t.Fatal("factories share state", ok, e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			j, e := f.New(ctx, empty{})
			if e != nil {
				t.Error(e)
				return
			}
			if e = j.Set(ctx, "x", []byte("v")); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
}
