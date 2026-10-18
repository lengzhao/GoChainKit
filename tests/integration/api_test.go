package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	apihttp "github.com/lengzhao/GoChainKit/api/http"
	"github.com/lengzhao/GoChainKit/assembly"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"github.com/lengzhao/pluginkit/build"
	"github.com/lengzhao/pluginkit/config"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var apiClient = &http.Client{Timeout: 3 * time.Second}

func apiCall(t *testing.T, url, method string, body any, status int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		reader = bytes.NewReader(data)
	}
	req, e := http.NewRequest(method, url, reader)
	if e != nil {
		t.Fatal(e)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := apiClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != status {
		t.Fatalf("%s %s: %d %s", method, url, resp.StatusCode, data)
	}
	var v map[string]any
	if e = json.Unmarshal(data, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func startAPIGraph(t *testing.T, graph map[string]any) (string, *build.Result, func()) {
	t.Helper()
	root, result, e := build.Build[core.Node](context.Background(), graph, "node")
	if e != nil {
		t.Fatal(e)
	}
	server, e := build.RequireByID[httpcap.Server](result, "api", "api")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() { runErr = root.Run(ctx); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
				if runErr != nil {
					t.Error(runErr)
				}
			case <-time.After(8 * time.Second):
				t.Error("node stop timed out")
			}
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(5 * time.Second)
	for len(server.Addresses()) == 0 {
		select {
		case <-done:
			cancel()
			t.Fatalf("node exited: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("API start timeout")
		}
		time.Sleep(time.Millisecond)
	}
	return server.Addresses()[0], result, stop
}
func addAPI(graph map[string]any, ledger string) {
	graph["api-query"] = config.PluginUse{Use: "api/query", Deps: map[string]any{"protocol": "protocol", "ledger": ledger, "pool": "pool"}}
	graph["api"] = config.PluginUse{Use: "api/http", Config: map[string]any{"listen": "127.0.0.1:0"}, Deps: map[string]any{"query": "api-query"}}
	graph["consensus"] = config.PluginUse{Use: "consensus/single", Config: map[string]any{"interval": "20ms"}}
	graph["node"] = config.PluginUse{Use: "node/default", Config: map[string]any{"shutdownTimeout": "5s"}, Deps: map[string]any{"ledger": ledger, "host": "host", "consensus": "consensus", "resources": []any{"api"}, "workers": []any{"api"}}}
}
func waitConfirmed(t *testing.T, url, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for {
		v := apiCall(t, url+"/v1/transactions/"+id, "GET", nil, 200)
		if v["status"] == "confirmed" {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatal("confirmation timeout", v)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestAPIProtocolSubmissionAndReopen(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			graph := productionGraph(config.PluginUse{Use: "storage/bolt", Config: map[string]any{"path": filepath.Join(t.TempDir(), "chain.db")}}, custom)
			addAPI(graph, "store")
			url, result, stop := startAPIGraph(t, graph)
			p, e := build.RequireByID[core.Protocol](result, "protocol", "protocol")
			if e != nil {
				t.Fatal(e)
			}
			tx := defaultTransaction(t, 1)
			if custom {
				tx = testprotocol.Tx{Label: "binary", Body: []byte{0xff, 0, 0x80}}
			}
			data, e := p.EncodeTransaction(tx)
			if e != nil {
				t.Fatal(e)
			}
			id := apihttp.Hex(tx.ID())
			a := apiCall(t, url+"/v1/transactions", "POST", map[string]any{"data": data}, 202)
			if a["id"] != id || a["status"] != "accepted" {
				t.Fatal(a)
			}
			confirmed := waitConfirmed(t, url, id)
			raw, e := base64.StdEncoding.DecodeString(confirmed["data"].(string))
			if e != nil {
				t.Fatal(e)
			}
			decoded, e := p.DecodeTransaction(raw)
			if e != nil || decoded.ID() != tx.ID() {
				t.Fatal("transaction wire changed", e)
			}
			block := apiCall(t, url+"/v1/blocks/"+confirmed["blockId"].(string), "GET", nil, 200)
			raw, e = base64.StdEncoding.DecodeString(block["data"].(string))
			if e != nil {
				t.Fatal(e)
			}
			b, e := p.DecodeBlock(raw)
			if e != nil || apihttp.Hex(b.ID()) != block["id"] {
				t.Fatal("block wire changed", e)
			}
			// The custom protocol intentionally emits no receipts.
			receiptStatus := 200
			if custom {
				receiptStatus = 404
			}
			apiCall(t, url+"/v1/receipts/"+id, "GET", nil, receiptStatus)
			apiCall(t, url+"/v1/transactions", "POST", map[string]any{"data": data}, 200)
			stop()
			graph["consensus"] = config.PluginUse{Use: "consensus/single", Config: map[string]any{"interval": "1h"}}
			url, _, stop = startAPIGraph(t, graph)
			recovered := apiCall(t, url+"/v1/transactions/"+id, "GET", nil, 200)
			if recovered["blockId"] != confirmed["blockId"] || recovered["status"] != "confirmed" {
				t.Fatal(recovered)
			}
			head := apiCall(t, url+"/v1/chain/head", "GET", nil, 200)
			if head["id"] == "" {
				t.Fatal(head)
			}
			stop()
		})
	}
}
func TestAPIModelTransfersAndSnapshots(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	sender := asset.AddressOf(key.Public().(ed25519.PublicKey))
	recipient := assetcap.Address{2}
	const supply = uint64(1 << 54)
	for _, name := range []string{"account", "utxo"} {
		t.Run(name, func(t *testing.T) {
			data, e := os.ReadFile("../../presets/pow-" + name + ".yaml")
			if e != nil {
				t.Fatal(e)
			}
			var cfg assembly.Config
			if e = yaml.Unmarshal(data, &cfg); e != nil {
				t.Fatal(e)
			}
			// Modify through the YAML envelope, preserving the real production plugin graph.
			graph := cfg.Plugins
			plugin := func(id string) map[string]any { return graph[id].(map[string]any) }
			dir := t.TempDir()
			plugin("ledger")["config"] = map[string]any{"path": filepath.Join(dir, "chain.db")}
			plugin("network")["config"] = map[string]any{"identityPath": filepath.Join(dir, "peer.key"), "listen": []string{"/ip4/127.0.0.1/tcp/0"}}
			plugin("api")["config"] = map[string]any{"listen": "127.0.0.1:0"}
			plugin("consensus")["config"] = map[string]any{"interval": "20ms"}
			network := "composed-" + name
			plugin("model")["config"] = map[string]any{"Network": network, "Allocations": []assetcap.Allocation{{Address: sender, Amount: supply}}}
			plugin("rule")["config"] = map[string]any{"retargetInterval": 2016, "targetSpacing": 10, "initialReward": 50}
			url, result, stop := startAPIGraph(t, graph)
			p, e := build.RequireByID[core.Protocol](result, "protocol", "protocol")
			if e != nil {
				t.Fatal(e)
			}
			genesis := apiCall(t, url+"/v1/chain/head", "GET", nil, 200)["id"].(string)
			var tx core.Transaction
			var oldPoint utxo.Outpoint
			if name == "account" {
				initial := apiCall(t, url+"/v1/accounts/"+hex.EncodeToString(sender[:])+"?at="+genesis, "GET", nil, 200)
				if initial["balance"] != "18014398509481984" || initial["nonce"] != "0" {
					t.Fatal("precision lost", initial)
				}
				raw := account.Transaction{Network: network, Nonce: 1, To: recipient, Amount: 25}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = account.Wrap(raw)
			} else {
				engine, e := build.RequireByID[*utxo.Engine](result, "model", "model")
				if e != nil {
					t.Fatal(e)
				}
				oldPoint = engine.GenesisOutpoints()[0]
				raw := utxo.Transaction{Network: network, Inputs: []utxo.Input{{Previous: oldPoint}}, Outputs: []utxo.Output{{To: recipient, Amount: 25}, {To: sender, Amount: supply - 25}}}
				if e = raw.Sign(key); e != nil {
					t.Fatal(e)
				}
				tx, e = utxo.Wrap(raw)
			}
			if e != nil {
				t.Fatal(e)
			}
			encoded, e := p.EncodeTransaction(tx)
			if e != nil {
				t.Fatal(e)
			}
			id := apihttp.Hex(tx.ID())
			apiCall(t, url+"/v1/transactions", "POST", map[string]any{"data": encoded}, 202)
			confirmed := waitConfirmed(t, url, id)
			check := func(url string) {
				if name == "account" {
					v := apiCall(t, url+"/v1/accounts/"+hex.EncodeToString(recipient[:]), "GET", nil, 200)
					if v["balance"] != "25" || v["at"] == "" {
						t.Fatal(v)
					}
					v = apiCall(t, url+"/v1/accounts/"+hex.EncodeToString(sender[:]), "GET", nil, 200)
					if v["balance"] != "18014398509481959" || v["nonce"] != "1" {
						t.Fatal(v)
					}
					v = apiCall(t, url+"/v1/accounts/"+hex.EncodeToString(sender[:])+"?at="+genesis, "GET", nil, 200)
					if v["balance"] != "18014398509481984" || v["nonce"] != "0" || v["at"] != genesis {
						t.Fatal(v)
					}
				} else {
					v := apiCall(t, url+"/v1/utxos/"+id+"/0", "GET", nil, 200)
					if v["amount"] != "25" || v["owner"] != hex.EncodeToString(recipient[:]) || v["index"] != "0" {
						t.Fatal(v)
					}
					oldPath := "/v1/utxos/" + hex.EncodeToString(oldPoint.TransactionID[:]) + "/0"
					apiCall(t, url+oldPath, "GET", nil, 404)
					v = apiCall(t, url+oldPath+"?at="+genesis, "GET", nil, 200)
					if v["amount"] != "18014398509481984" || v["at"] != genesis {
						t.Fatal(v)
					}
					apiCall(t, url+"/v1/utxos/"+id+"/4294967296", "GET", nil, 400)
				}
				apiCall(t, url+"/v1/receipts/"+id, "GET", nil, 200)
			}
			check(url)
			stop()
			plugin("consensus")["config"] = map[string]any{"interval": "1h", "mining": false}
			url, _, stop = startAPIGraph(t, graph)
			check(url)
			recovered := apiCall(t, url+"/v1/transactions/"+id, "GET", nil, 200)
			if recovered["blockId"] != confirmed["blockId"] {
				t.Fatal(recovered)
			}
			stop()
		})
	}
}
