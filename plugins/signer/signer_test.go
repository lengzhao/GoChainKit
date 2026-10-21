package signer_test

import (
	"context"
	"crypto/ed25519"
	store "github.com/lengzhao/GoChainKit/cap/keystore"
	cap "github.com/lengzhao/GoChainKit/cap/signer"
	member "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/keystore"
	_ "github.com/lengzhao/GoChainKit/plugins/signer"
	_ "github.com/lengzhao/GoChainKit/plugins/validator"
	"github.com/lengzhao/pluginkit"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"testing"
)

func init() {
	pluginkit.Register("test/signer-store", func(c struct {
		Seed byte `json:"seed"`
	}, _ struct{}) (*keystore.Memory, error) {
		seed := make([]byte, 32)
		seed[0] = c.Seed
		key := ed25519.NewKeyFromSeed(seed)
		defer clear(key)
		memory := keystore.NewMemory()
		if err := memory.Create(context.Background(), store.ID("k"), key); err != nil {
			return nil, err
		}
		return memory, nil
	})
}
func TestTypedGraph(t *testing.T) {
	ctx := context.Background()
	graph := map[string]any{
		"a":  config.PluginUse{Use: "test/signer-store", Config: map[string]any{"seed": 0}},
		"b":  config.PluginUse{Use: "test/signer-store", Config: map[string]any{"seed": 1}},
		"sa": config.PluginUse{Use: "signer/ed25519-keystore", Config: map[string]any{"keyID": "k"}, Deps: map[string]any{"store": "a"}},
		"sb": config.PluginUse{Use: "signer/ed25519-keystore", Config: map[string]any{"keyID": "k"}, Deps: map[string]any{"store": "b"}},
		"v":  config.PluginUse{Use: "signer/ed25519-verifier"},
		"members": config.PluginUse{Use: "validator/static", Config: map[string]any{
			"members": []member.Member{{ID: "a", Key: cap.PublicKey{Algorithm: "external/v1", Bytes: []byte{1}}}},
		}},
	}
	sa, _, e := build.Build[cap.Signer](ctx, graph, "sa")
	if e != nil {
		t.Fatal(e)
	}
	sb, _, e := build.Build[cap.Signer](ctx, graph, "sb")
	if e != nil {
		t.Fatal(e)
	}
	pa, _ := sa.PublicKey(ctx)
	pb, _ := sb.PublicKey(ctx)
	if string(pa.Bytes) == string(pb.Bytes) {
		t.Fatal("instances share store")
	}
	v, _, e := build.Build[cap.Verifier](ctx, graph, "v")
	if e != nil {
		t.Fatal(e)
	}
	d := cap.Domain{Chain: "c", Purpose: "p", Version: "1"}
	sig, _ := sa.Sign(ctx, d, nil)
	if e = v.Verify(ctx, pa, d, nil, sig); e != nil {
		t.Fatal(e)
	}
	if _, _, e = build.Build[member.Provider](ctx, graph, "members"); e != nil {
		t.Fatal(e)
	}
}
