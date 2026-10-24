package utxo

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/lengzhao/GoChainKit/api"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/pow"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddressBalanceAnchoredAndPaged(t *testing.T) {
	owner := assetcap.Address{1}
	engine, _ := utxo.New(utxo.Config{Network: "api", Allocations: []assetcap.Allocation{{Address: owner, Amount: 30}, {Address: owner, Amount: 40}}})
	rule, _ := pow.New(pow.Config{})
	p, err := composed.NewPoW(composed.Config{Network: "api", GenesisTime: 1700000000}, engine, rule, rule)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	query, _ := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ledger, Pool: pool})
	routes, err := New(Config{}, Deps{Protocol: p, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, r := range routes.Routes() {
		mux.Handle(r.Pattern, r.Handler)
	}
	path := "/v1/addresses/" + hex.EncodeToString(owner[:]) + "/utxos?limit=1"
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest("GET", path, nil))
	if res.Code != 200 {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	var balance Balance
	if err = json.Unmarshal(res.Body.Bytes(), &balance); err != nil {
		t.Fatal(err)
	}
	head, _ := ledger.Head(context.Background())
	if balance.At != hex.EncodeToString([]byte(head)) || balance.Height != 0 || balance.Spendable != 70 || balance.Immature != 0 || len(balance.Coins) != 1 || balance.NextOffset != 1 {
		t.Fatal(balance)
	}
	for _, suffix := range []string{"?limit=0", "?limit=10000", "?offset=-1"} {
		res = httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest("GET", "/v1/addresses/"+hex.EncodeToString(owner[:])+"/utxos"+suffix, nil))
		if res.Code != 400 {
			t.Fatal(res.Code)
		}
	}
	res = httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest("GET", "/v1/addresses/bad/utxos", nil))
	if res.Code != 400 {
		t.Fatal(res.Code)
	}
}
