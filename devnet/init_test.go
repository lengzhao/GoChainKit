package devnet

import (
	"context"
	"github.com/lengzhao/GoChainKit/assembly"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitializeThreeIndependentNodesAndReuse(t *testing.T) {
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "test-password")
	ctx := context.Background()
	root := t.TempDir()
	cfg := Config{Directory: filepath.Join(root, "network"), DataRoot: filepath.Join(root, "data"), GenesisTime: time.Now().Unix()}
	manifest, err := Initialize(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Nodes) != 3 {
		t.Fatal(manifest)
	}
	peers, addresses := map[string]bool{}, map[string]bool{}
	for _, node := range manifest.Nodes {
		if peers[node.PeerID] || addresses[node.Address] {
			t.Fatal("identity reused")
		}
		peers[node.PeerID] = true
		addresses[node.Address] = true
		file, err := os.Open(node.Config)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = assembly.Load(ctx, file)
		file.Close()
		if err != nil {
			t.Fatal("configuration does not assemble", err)
		}
	}
	again, err := Initialize(ctx, cfg)
	if err != nil || again.Nodes[0] != manifest.Nodes[0] {
		t.Fatal("restart changed identity", again, err)
	}
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "wrong-password")
	if _, err = Initialize(ctx, cfg); err == nil {
		t.Fatal("wrong password accepted")
	}
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "test-password")
	if err = os.WriteFile(manifest.Nodes[0].Config, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Initialize(ctx, cfg); err == nil {
		t.Fatal("modified config accepted")
	}
}

func TestPartialInitializationRefused(t *testing.T) {
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "test-password")
	root := t.TempDir()
	directory := filepath.Join(root, "network")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "node1.yaml"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Initialize(context.Background(), Config{Directory: directory, DataRoot: filepath.Join(root, "data"), GenesisTime: 1}); err == nil {
		t.Fatal("partial initialization accepted")
	}
}
