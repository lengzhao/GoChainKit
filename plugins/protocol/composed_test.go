package protocol_test

import (
	"context"
	"fmt"
	"github.com/lengzhao/GoChainKit/assembly"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposedModelsAssembleFromConfig(t *testing.T) {
	for _, model := range []string{"account", "utxo"} {
		t.Run(model, func(t *testing.T) {
			cfg := fmt.Sprintf(`root: node
plugins:
  model: {use: model/%s, config: {Network: composition}}
  rule: {use: rules/pow, config: {retargetInterval: 4, targetSpacing: 10, initialReward: 50}}
  protocol:
    use: protocol/composed-pow
    config: {network: composition, genesisTime: 1700000000, producer: '0300000000000000000000000000000000000000000000000000000000000000'}
    deps: {model: model, rule: rule, choice: rule}
  ledger: {use: storage/bolt, config: {path: %s}, deps: {protocol: protocol}}
  executor: {use: executor/composed, deps: {protocol: protocol, history: ledger}}
  pool: {use: mempool/memory, config: {maxTransactions: 100, maxBytes: 2097152}, deps: {protocol: protocol}}
  selector: {use: selector/fifo, deps: {protocol: protocol}}
  builder: {use: builder/basic, config: {maxTransactionBytes: 1048576}, deps: {protocol: protocol, ledger: ledger, pool: pool, selector: selector, executor: executor}}
  host: {use: node/coordinator, deps: {protocol: protocol, ledger: ledger, builder: builder, executor: executor, pool: pool}}
  network: {use: network/libp2p, config: {identityPath: %s, listen: [/ip4/127.0.0.1/tcp/0], seeds: []}, deps: {protocol: protocol}}
  importer: {use: importer/branch, deps: {protocol: protocol, ledger: ledger, executor: executor, pool: pool}}
  replication: {use: sync/chain, config: {interval: 1s}, deps: {protocol: protocol, ledger: ledger, pool: pool, network: network, importer: importer}}
  consensus: {use: consensus/pow, config: {interval: 1s}, deps: {protocol: protocol, ledger: ledger, builder: builder, importer: importer, replication: replication}}
  node: {use: node/default, config: {shutdownTimeout: 5s}, deps: {ledger: ledger, host: host, consensus: consensus, resources: [network]}}
`, model, filepath.Join(t.TempDir(), "chain.db"), filepath.Join(t.TempDir(), "peer.key"))
			node, results, e := assembly.Load(context.Background(), strings.NewReader(cfg))
			if e != nil {
				t.Fatal(e)
			}
			if node == nil || results == nil {
				t.Fatal("missing root")
			}
		})
	}
}
