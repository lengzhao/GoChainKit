package native_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/lengzhao/GoChainKit/api"
	native "github.com/lengzhao/GoChainKit/api/http/native"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/internal/testbackend"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
	"github.com/lengzhao/GoChainKit/model/evm"
	pool "github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/pow"
)

func deps(t *testing.T, e model.Engine) native.Deps {
	t.Helper()
	ctx := context.Background()
	rule, err := pow.New(pow.Config{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := composed.NewPoW(composed.Config{Network: e.Protocol().Network, GenesisTime: 1700000000}, e, rule, rule)
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.NewKV(storage.KVConfig{}, storage.KVDeps{Protocol: p, Backend: testbackend.Bolt(t, filepath.Join(t.TempDir(), "chain.db"))})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop(ctx) })
	m, err := pool.NewMemory(pool.MemoryConfig{MaxTransactions: 10, MaxBytes: 1 << 20}, pool.Deps{Protocol: p})
	if err != nil {
		t.Fatal(err)
	}
	q, err := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: s, Pool: m})
	if err != nil {
		t.Fatal(err)
	}
	return native.Deps{Protocol: p, Query: q}
}
func TestBitcoinQueries(t *testing.T) {
	e, err := btc.New(btc.Config{Network: "query-bitcoin", GenesisTime: 1700000000, Allocations: []btc.Allocation{{Value: 1000, Script: []byte{0x51}}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := native.NewBitcoin(native.Config{}, deps(t, e))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, route := range r.Routes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/bitcoin/genesis", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	point := e.GenesisOutpoints()[0]
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/bitcoin/utxos/"+point.Hash.String()+"/0", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"value":"1000"`) {
		t.Fatalf("query: %d %s", w.Code, w.Body.String())
	}
	if _, err = native.NewEVM(native.Config{}, deps(t, e)); err == nil {
		t.Fatal("wrong model accepted")
	}
}
func TestEVMQueriesAndReadOnlyCall(t *testing.T) {
	address := common.Address{1}
	e, err := evm.New(evm.Config{Network: "query-evm", ChainID: "1337", Allocations: map[string]evm.Account{address.Hex(): {Balance: "1000", Code: []byte{0x60, 0x2a, 0x60, 0, 0x52, 0x60, 0x20, 0x60, 0, 0xf3}}}})
	if err != nil {
		t.Fatal(err)
	}
	d := deps(t, e)
	r, err := native.NewEVM(native.Config{}, d)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	for _, route := range r.Routes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	for _, path := range []string{"/v1/evm/accounts/" + address.Hex(), "/v1/evm/storage/" + address.Hex() + "/" + common.Hash{}.Hex()} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	data, _ := json.Marshal(map[string]any{"to": address.Hex(), "gas": "100000"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/v1/evm/call", bytes.NewReader(data)))
	if w.Code != 200 {
		t.Fatalf("call %d %s", w.Code, w.Body.String())
	}
	var out struct{ Data []byte }
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Data) != 32 || out.Data[31] != 42 {
		t.Fatalf("call data: %x %v", out.Data, err)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/v1/evm/call", strings.NewReader(`{"to":"bad","unknown":1}`)))
	if w.Code != 400 {
		t.Fatalf("invalid call %d", w.Code)
	}
	bad, _ := json.Marshal(map[string]any{"to": address.Hex(), "value": "-1"})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/v1/evm/call", bytes.NewReader(bad)))
	if w.Code != 400 {
		t.Fatalf("invalid call amount returned %d", w.Code)
	}
	lowGas,_:=json.Marshal(map[string]any{"to":address.Hex(),"gas":"1"})
	w=httptest.NewRecorder();mux.ServeHTTP(w,httptest.NewRequest("POST","/v1/evm/call",bytes.NewReader(lowGas)))
	if w.Code!=400{t.Fatalf("intrinsic gas error returned %d",w.Code)}
}

func TestContractExecutionFailureIsNotServerFailure(t *testing.T){
	address:=common.Address{3}
	e,err:=evm.New(evm.Config{Network:"invalid-opcode",ChainID:"1337",Allocations:map[string]evm.Account{address.Hex():{Code:[]byte{0xfe}}}});if err!=nil{t.Fatal(err)}
	r,err:=native.NewEVM(native.Config{},deps(t,e));if err!=nil{t.Fatal(err)}
	mux:=http.NewServeMux();for _,route:=range r.Routes(){mux.Handle(route.Pattern,route.Handler)}
	data,_:=json.Marshal(map[string]any{"to":address.Hex(),"gas":"100000"})
	w:=httptest.NewRecorder();mux.ServeHTTP(w,httptest.NewRequest("POST","/v1/evm/call",bytes.NewReader(data)))
	if w.Code!=422{t.Fatalf("invalid opcode returned %d: %s",w.Code,w.Body.String())}
}
