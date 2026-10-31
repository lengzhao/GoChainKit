// gochainkit-authority-init creates an operator-owned, single-authority development chain.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/plugins/secret"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := flag.String("directory", "data/authority", "new directory (must not exist)")
	flag.Parse()
	path, e := initialize(context.Background(), *dir, time.Now().Unix())
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("Configuration:", path)
}

// initialize refuses to merge with an existing directory, including symlinks.
// On failure it leaves any created data for operator inspection, never deletes it.
func initialize(ctx context.Context, dir string, genesis int64) (string, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	source, _ := secret.NewEnvironment(secret.Config{Environment: "GOCHAINKIT_KEYSTORE_PASSWORD"}, struct{}{})
	password, e := source.Secret(ctx)
	clear(password)
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
		return "", e
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return "", e
	}
	defer clear(key)
	keys, e := keystore.NewEncryptedFile(keystore.FileConfig{Directory: filepath.Join(dir, "keys")}, source)
	if e != nil {
		return "", e
	}
	if e = keys.Create(ctx, "consensus", key); e != nil {
		return "", e
	}
	id := hex.EncodeToString(pub)
	member := valcap.Member{ID: core.ID(id), Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: pub}}
	cfg := configuration(dir, genesis, member)
	data, e := yaml.Marshal(cfg)
	if e != nil {
		return "", e
	}
	path := filepath.Join(dir, "node.yaml")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if syncErr != nil {
		return "", syncErr
	}
	return path, closeErr
}
func plugin(use string, config, deps any) map[string]any {
	p := map[string]any{"use": use}
	if config != nil {
		p["config"] = config
	}
	if deps != nil {
		p["deps"] = deps
	}
	return p
}
func configuration(dir string, genesis int64, member valcap.Member) map[string]any {
	m := func(kv ...string) map[string]string {
		out := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	p := map[string]any{
		"model":       plugin("model/account", map[string]any{"Network": "authority-dev"}, nil),
		"members":     plugin("validator/static", map[string]any{"members": []valcap.Member{member}}, nil),
		"verifier":    plugin("signer/ed25519-verifier", nil, nil),
		"rule":        plugin("rules/poa", map[string]any{"genesisTime": genesis, "slotSeconds": 2, "epochSlots": 100}, m("members", "members", "verifier", "verifier")),
		"choice":      plugin("choice/authority", nil, nil),
		"protocol":    plugin("protocol/composed", map[string]any{"network": "authority-dev", "genesisTime": genesis, "producer": hex.EncodeToString([]byte(member.ID))}, m("model", "model", "rule", "rule", "choice", "choice")),
		"backend":     plugin("backend/bolt", map[string]any{"path": filepath.Join(dir, "chain.db")}, nil),
		"ledger":      plugin("storage/kv", nil, m("protocol", "protocol", "backend", "backend")),
		"pool":        plugin("mempool/memory", map[string]any{"maxTransactions": 1000, "maxBytes": 16777216}, m("protocol", "protocol")),
		"executor":    plugin("executor/composed", nil, m("protocol", "protocol", "history", "ledger")),
		"selector":    plugin("selector/fifo", nil, m("protocol", "protocol")),
		"builder":     plugin("builder/basic", map[string]any{"maxTransactionBytes": 1048576}, m("protocol", "protocol", "ledger", "ledger", "pool", "pool", "executor", "executor", "selector", "selector")),
		"host":        plugin("node/coordinator", nil, m("protocol", "protocol", "ledger", "ledger", "builder", "builder", "executor", "executor", "pool", "pool")),
		"key-secret":  plugin("secret/env", map[string]any{"environment": "GOCHAINKIT_KEYSTORE_PASSWORD"}, nil),
		"keys":        plugin("keystore/encrypted-file", map[string]any{"directory": filepath.Join(dir, "keys")}, m("secret", "key-secret")),
		"signer":      plugin("signer/ed25519-keystore", map[string]any{"keyID": "consensus"}, m("store", "keys")),
		"producer":    plugin("seal/authority", nil, m("rule", "rule", "signer", "signer")),
		"identity":    plugin("identity/libp2p", map[string]any{"keyID": "p2p"}, m("store", "keys")),
		"network":     plugin("network/libp2p", map[string]any{"listen": []string{"/ip4/127.0.0.1/tcp/0"}, "seeds": []string{}}, m("protocol", "protocol", "identity", "identity")),
		"importer":    plugin("importer/branch", nil, m("protocol", "protocol", "ledger", "ledger", "executor", "executor", "pool", "pool")),
		"router":      plugin("message/router", map[string]any{}, m("network", "network")),
		"replication": plugin("sync/chain", map[string]any{"interval": "200ms"}, m("protocol", "protocol", "ledger", "ledger", "pool", "pool", "network", "network", "router", "router", "importer", "importer")),
		"consensus":   plugin("consensus/authority", map[string]any{"interval": "200ms"}, m("protocol", "protocol", "ledger", "ledger", "builder", "builder", "importer", "importer", "replication", "replication", "producer", "producer")),
		"api-query":   plugin("api/query", nil, m("protocol", "protocol", "ledger", "ledger", "pool", "pool")),
		"api-model":   plugin("api/account", nil, m("protocol", "protocol", "query", "api-query")),
		"api":         plugin("api/http", map[string]any{"listen": "127.0.0.1:0"}, map[string]any{"query": "api-query", "network": "network", "modules": []string{"api-model"}}),
		"node":        plugin("node/default", map[string]any{"shutdownTimeout": "5s"}, map[string]any{"ledger": "ledger", "host": "host", "consensus": "consensus", "resources": []string{"network", "router", "api"}, "workers": []string{"api"}}),
	}
	return map[string]any{"root": "node", "plugins": p}
}
