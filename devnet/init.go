// Package devnet generates a private, persistent three-node currency network.
package devnet

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/lengzhao/GoChainKit/identity/libp2p"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/rules/pow"
	"github.com/lengzhao/GoChainKit/wallet"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"time"
)

//go:embed template.yaml
var template []byte

const Network = "gochainkit-devnet-v1"

type Config struct {
	Directory, DataRoot string
	GenesisTime         int64
}
type Node struct {
	Name       string `json:"name"`
	PeerID     string `json:"peerId"`
	Address    string `json:"address"`
	Config     string `json:"config"`
	ConfigHash string `json:"configHash"`
}
type Manifest struct {
	Version     int    `json:"version"`
	Network     string `json:"network"`
	GenesisTime int64  `json:"genesisTime"`
	Nodes       []Node `json:"nodes"`
}

func emptyDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("not a real directory: %s", path)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("partial/existing initialization at %s; inspect it before using a new directory", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.MkdirAll(path, 0700)
}
func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}
func nodeStore(dataRoot, name string) (*keystore.EncryptedFile, error) {
	return keystore.NewEncryptedFile(keystore.FileConfig{Directory: filepath.Join(dataRoot, name, "keys")}, wallet.EnvironmentSecret{})
}

// Initialize creates once, or verifies an existing manifest and its keys/configs.
// Failure preserves partial state for inspection. It never replaces material.
func Initialize(ctx context.Context, c Config) (Manifest, error) {
	var out Manifest
	if c.Directory == "" || c.DataRoot == "" {
		return out, fmt.Errorf("directory and data root required")
	}
	var err error
	c.Directory, err = filepath.Abs(c.Directory)
	if err != nil {
		return out, err
	}
	c.DataRoot, err = filepath.Abs(c.DataRoot)
	if err != nil {
		return out, err
	}
	password, err := (wallet.EnvironmentSecret{}).Secret(ctx)
	clear(password)
	if err != nil {
		return out, err
	}
	path := filepath.Join(c.Directory, "manifest.json")
	if data, err := os.ReadFile(path); err == nil {
		if err = json.Unmarshal(data, &out); err != nil {
			return out, err
		}
		if out.Version != 1 || out.Network != Network || len(out.Nodes) != 3 {
			return out, fmt.Errorf("incompatible devnet manifest")
		}
		for i, node := range out.Nodes {
			name := fmt.Sprintf("node%d", i+1)
			if node.Name != name || node.Config != filepath.Join(c.Directory, name+".yaml") {
				return out, fmt.Errorf("invalid manifest paths")
			}
			data, err := os.ReadFile(node.Config)
			if err != nil {
				return out, err
			}
			hash := sha256.Sum256(data)
			if node.ConfigHash != hex.EncodeToString(hash[:]) {
				return out, fmt.Errorf("configuration modified: %s", node.Config)
			}
			store, err := nodeStore(c.DataRoot, name)
			if err != nil {
				return out, err
			}
			key, err := wallet.Load(ctx, store, "wallet")
			if err != nil {
				return out, err
			}
			address := asset.AddressOf(key[32:])
			clear(key)
			if hex.EncodeToString(address[:]) != node.Address {
				return out, fmt.Errorf("wallet does not match manifest")
			}
			raw, err := store.Get(ctx, "p2p")
			if err != nil {
				return out, err
			}
			pkey, err := crypto.UnmarshalPrivateKey(raw)
			clear(raw)
			if err != nil {
				return out, err
			}
			id, err := peer.IDFromPrivateKey(pkey)
			if err != nil {
				return out, err
			}
			if id.String() != node.PeerID {
				return out, fmt.Errorf("P2P identity does not match manifest")
			}
		}
		return out, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	if err = emptyDirectory(c.Directory); err != nil {
		return out, err
	}
	if err = writeExclusive(filepath.Join(c.Directory, "initializing"), []byte("Initialization incomplete until manifest.json exists.\n")); err != nil {
		return out, err
	}
	if c.GenesisTime == 0 {
		c.GenesisTime = time.Now().Unix() - 1
	}
	if c.GenesisTime < 1 {
		return out, fmt.Errorf("positive genesis time required")
	}
	out = Manifest{Version: 1, Network: Network, GenesisTime: c.GenesisTime}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("node%d", i)
		if err = emptyDirectory(filepath.Join(c.DataRoot, name)); err != nil {
			return out, err
		}
		store, err := nodeStore(c.DataRoot, name)
		if err != nil {
			return out, err
		}
		address, err := wallet.Create(ctx, store, "wallet")
		if err != nil {
			return out, err
		}
		provider, err := identity.New(identity.Config{KeyID: "p2p"}, identity.Deps{Store: store})
		if err != nil {
			return out, err
		}
		raw, err := provider.PrivateKey(ctx)
		if err != nil {
			return out, err
		}
		key, err := crypto.UnmarshalPrivateKey(raw)
		clear(raw)
		if err != nil {
			return out, err
		}
		id, err := peer.IDFromPrivateKey(key)
		if err != nil {
			return out, err
		}
		out.Nodes = append(out.Nodes, Node{Name: name, PeerID: id.String(), Address: hex.EncodeToString(address[:]), Config: filepath.Join(c.Directory, name+".yaml")})
	}
	for i, node := range out.Nodes {
		data, err := configuration(c, out, node)
		if err != nil {
			return out, err
		}
		if err = writeExclusive(node.Config, data); err != nil {
			return out, err
		}
		hash := sha256.Sum256(data)
		out.Nodes[i].ConfigHash = hex.EncodeToString(hash[:])
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return out, err
	}
	if err = writeExclusive(path, data); err != nil {
		return out, err
	}
	return out, nil
}

func configuration(c Config, m Manifest, node Node) ([]byte, error) {
	var graph map[string]any
	if err := yaml.Unmarshal(template, &graph); err != nil {
		return nil, err
	}
	plugins := graph["plugins"].(map[string]any)
	config := func(id string, value any) { plugins[id].(map[string]any)["config"] = value }
	data := filepath.Join(c.DataRoot, node.Name)
	config("model", utxo.Config{Network: Network, CoinbaseMaturity: 5})
	config("rule", pow.Config{InitialBits: 0x207fffff, LimitBits: 0x207fffff, RetargetInterval: 20, TargetSpacing: 2, MedianWindow: 11, FutureDrift: 7200, InitialReward: 5000000000, HalvingInterval: 150, Currency: true})
	config("protocol", map[string]any{"network": Network, "genesisTime": m.GenesisTime, "producer": node.Address})
	config("backend", map[string]any{"path": filepath.Join(data, "chain.db")})
	config("keys", map[string]any{"directory": filepath.Join(data, "keys")})
	seeds := []string{}
	for _, other := range m.Nodes {
		if other.Name != node.Name {
			seeds = append(seeds, "/dns4/"+other.Name+"/tcp/19001/p2p/"+other.PeerID)
		}
	}
	config("network", map[string]any{"listen": []string{"/ip4/0.0.0.0/tcp/19001"}, "seeds": seeds})
	config("replication", map[string]any{"interval": "200ms"})
	// Different schedules reduce constant same-height races while keeping every
	// node an independent miner. They are local settings, not consensus inputs.
	intervals := map[string]string{"node1": "2s", "node2": "3100ms", "node3": "4300ms"}
	config("consensus", map[string]any{"interval": intervals[node.Name]})
	config("api", map[string]any{"listen": "0.0.0.0:8080"})
	// PluginKit consumes JSON field names. Normalize typed configs through JSON
	// before YAML encoding, including omitting nonserializable clock callbacks.
	raw, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	return yaml.Marshal(normalized)
}
