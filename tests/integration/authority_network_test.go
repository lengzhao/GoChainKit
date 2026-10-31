package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"github.com/lengzhao/GoChainKit/internal/routertest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lengzhao/GoChainKit/api"
	apihttp "github.com/lengzhao/GoChainKit/api/http"
	accountapi "github.com/lengzhao/GoChainKit/api/http/account"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	signcap "github.com/lengzhao/GoChainKit/cap/signer"
	valcap "github.com/lengzhao/GoChainKit/cap/validator"
	choice "github.com/lengzhao/GoChainKit/choice/authority"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	runtime "github.com/lengzhao/GoChainKit/node"
	"github.com/lengzhao/GoChainKit/plugins/builder"
	"github.com/lengzhao/GoChainKit/plugins/consensus"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/network"
	"github.com/lengzhao/GoChainKit/plugins/replication"
	"github.com/lengzhao/GoChainKit/plugins/selector"
	store "github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/poa"
	seal "github.com/lengzhao/GoChainKit/seal/authority"
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"github.com/lengzhao/GoChainKit/validator/static"
)

const authorityGenesis int64 = 1000000000

type authorityNode struct {
	ledger   *store.KVStore
	protocol *composed.Protocol
	network  *network.Transport
	api      *apihttp.Server
	cancel   context.CancelFunc
	done     chan struct{}
	err      error // published by close(done)
}

func authorityWait(t *testing.T, nodes []*authorityNode, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		for i, n := range nodes {
			select {
			case <-n.done:
				t.Fatalf("authority node %d exited: %v", i, n.err)
			default:
			}
		}
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("authority network condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func (n *authorityNode) stop(t *testing.T) {
	t.Helper()
	if n.cancel == nil {
		return
	}
	n.cancel()
	select {
	case <-n.done:
		if n.err != nil {
			t.Errorf("authority shutdown: %v", n.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("authority shutdown timeout")
	}
	n.cancel = nil
}
func startAuthorityNode(t *testing.T, path string, producer int, clock func() time.Time, keys []ed25519.PrivateKey, owner assetcap.Address) *authorityNode {
	t.Helper()
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	members := make([]valcap.Member, len(keys))
	for i, k := range keys {
		members[i] = valcap.Member{ID: core.ID(fmt.Sprintf("member-%d", i)), Key: signcap.PublicKey{Algorithm: suite.Algorithm, Bytes: k[32:]}}
	}
	roster, e := static.New(static.Config{Members: members})
	must(e)
	rule, e := poa.New(poa.Config{GenesisTime: authorityGenesis, SlotSeconds: 1, EpochSlots: 10}, poa.Deps{Members: roster, Verifier: &suite.Verifier{}})
	must(e)
	model, e := account.New(account.Config{Network: "authority-network", Allocations: []assetcap.Allocation{{Address: owner, Amount: 100}}})
	must(e)
	var id []byte
	if producer >= 0 {
		id = []byte(members[producer].ID)
	}
	p, e := composed.New(composed.Config{Network: "authority-network", GenesisTime: authorityGenesis, Producer: id, Clock: clock}, model, rule, &choice.Choice{})
	must(e)
	s, e := store.NewKV(store.KVConfig{}, store.KVDeps{Backend: testbackend.Bolt(t, filepath.Join(path, "chain.db")), Protocol: p})
	must(e)
	m, e := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 100, MaxBytes: 4 << 20}, pool.Deps{Protocol: p})
	must(e)
	ex, e := composed.NewExecutor(p, s)
	must(e)
	sel, e := selector.NewFIFO(selector.FIFOConfig{}, selector.Deps{Protocol: p})
	must(e)
	b, e := builder.NewBasic(builder.BasicConfig{MaxTransactionBytes: 1 << 20}, builder.Deps{Protocol: p, Ledger: s, Pool: m, Executor: ex, Selector: sel})
	must(e)
	n, e := network.NewLibp2p(network.Config{Listen: []string{"/ip4/127.0.0.1/tcp/0"}, Timeout: "3s"}, network.Deps{Protocol: p, Identity: newPeerIdentity()})
	must(e)
	host, e := runtime.NewCoordinator(runtime.CoordinatorDeps{Protocol: p, Ledger: s, Builder: b, Executor: ex, Pool: m})
	must(e)
	imp, e := replication.NewImporter(replication.ImportConfig{}, replication.ImportDeps{Rules: p, Ledger: s, Executor: ex, Pool: m})
	must(e)
	rep, e := replication.NewSync(replication.SyncConfig{Interval: "10ms"}, replication.SyncDeps{Rules: p, Ledger: s, Pool: m, Router: routertest.Start(t, n), Network: n, Importer: imp})
	must(e)
	var signing consensus.AuthorityProducer
	if producer >= 0 {
		secret := keystore.NewMemory()
		must(secret.Create(ctx, "authority", keys[producer]))
		signer, e := custody.New(custody.Config{KeyID: "authority"}, custody.Deps{Store: secret})
		must(e)
		signing, e = seal.New(seal.Config{Clock: clock}, seal.Deps{Rule: rule, Signer: signer})
		must(e)
	}
	c, e := consensus.NewAuthority(consensus.AuthorityConfig{Interval: "5ms"}, consensus.AuthorityDeps{Protocol: p, Ledger: s, Builder: b, Importer: imp, Replication: rep, Producer: signing})
	must(e)
	query, e := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: s, Pool: m})
	must(e)
	routes, e := accountapi.New(accountapi.Config{}, accountapi.Deps{Protocol: p, Query: query})
	must(e)
	http, e := apihttp.New(apihttp.Config{Listen: "127.0.0.1:0"}, apihttp.Deps{Query: query, Network: n, Modules: []httpcap.RouteProvider{routes}})
	must(e)
	runner, e := runtime.NewRunner(runtime.RunnerConfig{ShutdownTimeout: "5s"}, runtime.RunnerDeps{Ledger: s, Host: host, Consensus: c, Resources: []core.Lifecycle{n, http}, Workers: []runtime.Worker{http}})
	must(e)
	run, cancel := context.WithCancel(ctx)
	out := &authorityNode{ledger: s, protocol: p, network: n, api: http, cancel: cancel, done: make(chan struct{})}
	go func() { out.err = runner.Run(run); close(out.done) }()
	t.Cleanup(func() { out.stop(t) })
	authorityWait(t, []*authorityNode{out}, func() bool { return len(n.Addresses()) > 0 && len(http.Addresses()) > 0 })
	return out
}

func TestAuthorityThreeNodesHTTPForkAndObserverRecovery(t *testing.T) {
	ctx := context.Background()
	var now atomic.Int64
	now.Store(authorityGenesis)
	clock := func() time.Time { return time.Unix(now.Load(), 0) }
	keys := make([]ed25519.PrivateKey, 3)
	for i := range keys {
		seed := make([]byte, 32)
		seed[0] = byte(i + 1)
		keys[i] = ed25519.NewKeyFromSeed(seed)
	}
	sender := ed25519.NewKeyFromSeed(make([]byte, 32))
	owner := asset.AddressOf(sender[32:])
	recipient := assetcap.Address{9}
	dir := t.TempDir()
	nodes := make([]*authorityNode, 3)
	for i := range nodes {
		nodes[i] = startAuthorityNode(t, filepath.Join(dir, fmt.Sprint(i)), i, clock, keys, owner)
	}
	head := func(n *authorityNode) core.ID {
		h, e := n.ledger.Head(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return h
	}
	genesis := head(nodes[0])
	raw := account.Transaction{Network: "authority-network", Nonce: 1, To: recipient, Amount: 7}
	if e := raw.Sign(sender); e != nil {
		t.Fatal(e)
	}
	tx, e := account.Wrap(raw)
	if e != nil {
		t.Fatal(e)
	}
	wire, e := nodes[0].protocol.EncodeTransaction(tx)
	if e != nil {
		t.Fatal(e)
	}
	id := apihttp.Hex(tx.ID())
	apiCall(t, nodes[0].api.Addresses()[0]+"/v1/transactions", "POST", map[string]any{"data": wire}, 202)
	// Each disconnected validator produces at its assigned slot, on genesis.
	forks := make([]core.ID, 3)
	for i := range nodes {
		now.Store(authorityGenesis + int64(i+1))
		authorityWait(t, nodes, func() bool { return head(nodes[i]) != genesis })
		forks[i] = head(nodes[i])
		block, e := nodes[i].ledger.Block(ctx, forks[i])
		if e != nil {
			t.Fatal(e)
		}
		envelope, e := composed.Unwrap(block)
		if e != nil || envelope.Header.Timestamp != authorityGenesis+int64(i+1) || string(envelope.Header.Producer) != fmt.Sprintf("member-%d", i) || len(envelope.Proof) != ed25519.SignatureSize {
			t.Fatalf("invalid slot %d block: %+v, %v", i+1, envelope, e)
		}
	}
	if forks[0] == forks[1] || forks[1] == forks[2] || forks[0] == forks[2] {
		t.Fatal("independent validator forks were not distinct")
	}
	for i := 1; i < 3; i++ {
		if e := nodes[i].network.Connect(ctx, nodes[0].network.Addresses()[0]); e != nil {
			t.Fatal(e)
		}
	}
	winner := forks[0]
	for _, fork := range forks[1:] {
		if bytes.Compare([]byte(fork), []byte(winner)) < 0 {
			winner = fork
		}
	}
	authorityWait(t, nodes, func() bool { return head(nodes[0]) == winner && head(nodes[1]) == winner && head(nodes[2]) == winner })
	now.Store(authorityGenesis + 4)
	authorityWait(t, nodes, func() bool {
		for _, n := range nodes {
			h := head(n)
			b, e := n.ledger.Block(ctx, h)
			if e != nil {
				return false
			}
			v, e := composed.Unwrap(b)
			if e != nil || v.Header.Height < 2 {
				return false
			}
			if _, e = n.ledger.Transaction(ctx, tx.ID()); e != nil {
				return false
			}
		}
		return head(nodes[0]) == head(nodes[1]) && head(nodes[1]) == head(nodes[2])
	})
	target := head(nodes[0])
	var expectedRoot core.Commitment
	assertState := func(n *authorityNode) {
		t.Helper()
		s, e := n.ledger.Snapshot(ctx, target)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		a, e := account.Balance(ctx, s, owner)
		if e != nil || a != 93 {
			t.Fatalf("sender balance %d: %v", a, e)
		}
		a, e = account.Balance(ctx, s, recipient)
		if e != nil || a != 7 {
			t.Fatalf("recipient balance %d: %v", a, e)
		}
		b, e := n.ledger.Block(ctx, target)
		if e != nil {
			t.Fatal(e)
		}
		v, e := composed.Unwrap(b)
		if e != nil || len(v.Proof) != ed25519.SignatureSize {
			t.Fatalf("signed head: %v", e)
		}
		if !bytes.Equal([]byte(s.Root()), v.Header.StateRoot) {
			t.Fatal("snapshot root differs from signed header")
		}
		if expectedRoot == "" {
			expectedRoot = s.Root()
		} else if s.Root() != expectedRoot {
			t.Fatal("state roots differ across nodes/reopen")
		}
	}
	for _, n := range nodes {
		assertState(n)
	}
	assertAPI := func(n *authorityNode) {
		t.Helper()
		confirmation := apiCall(t, n.api.Addresses()[0]+"/v1/transactions/"+id, "GET", nil, 200)
		if confirmation["status"] != "confirmed" || confirmation["id"] != id {
			t.Fatal("transaction not confirmed", confirmation)
		}
		receipt := apiCall(t, n.api.Addresses()[0]+"/v1/receipts/"+id, "GET", nil, 200)
		if receipt["id"] != id || receipt["blockId"] != confirmation["blockId"] || receipt["index"] != confirmation["index"] {
			t.Fatal("receipt confirmation mismatch", receipt, confirmation)
		}
		balance := apiCall(t, n.api.Addresses()[0]+"/v1/accounts/"+hex.EncodeToString(recipient[:]), "GET", nil, 200)
		if balance["balance"] != "7" || balance["at"] != apihttp.Hex(target) {
			t.Fatal("unanchored/wrong balance", balance)
		}
	}
	for _, n := range nodes {
		assertAPI(n)
	}
	nodes[2].stop(t)
	now.Store(authorityGenesis + 5)
	authorityWait(t, nodes[:2], func() bool { return head(nodes[0]) != target && head(nodes[0]) == head(nodes[1]) })
	target = head(nodes[0])
	// Skip absent member c's slot. a can still advance at slot 7.
	now.Store(authorityGenesis + 7)
	authorityWait(t, nodes[:2], func() bool { return head(nodes[0]) != target && head(nodes[0]) == head(nodes[1]) })
	target = head(nodes[0])
	nodes[2] = startAuthorityNode(t, filepath.Join(dir, "2"), -1, clock, keys, owner)
	if e := nodes[2].network.Connect(ctx, nodes[0].network.Addresses()[0]); e != nil {
		t.Fatal(e)
	}
	authorityWait(t, nodes, func() bool { return head(nodes[2]) == target })
	for _, n := range nodes {
		assertState(n)
		n.stop(t)
	}
	for i := range nodes {
		nodes[i] = startAuthorityNode(t, filepath.Join(dir, fmt.Sprint(i)), -1, clock, keys, owner)
		if head(nodes[i]) != target {
			t.Fatal("reopened canonical head changed")
		}
		assertState(nodes[i])
		assertAPI(nodes[i])
	}
}
