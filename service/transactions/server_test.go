package transactions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/lengzhao/GoChainKit/adapter/bitcoin"
	"github.com/lengzhao/GoChainKit/adapter/ethereum"
	"github.com/lengzhao/GoChainKit/assembly"
	_ "github.com/lengzhao/GoChainKit/plugins/transactions"
	service "github.com/lengzhao/GoChainKit/service/transactions"
)

func call(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data)))
	return w
}

func TestNativeHTTPServices(t *testing.T) {
	btc, _ := bitcoin.New(bitcoin.Config{})
	eth, _ := ethereum.New(ethereum.Config{ChainID: "1337"})
	s, err := service.New(service.Config{}, service.Deps{Bitcoin: btc, Ethereum: eth})
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Index: 1}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{txscript.OP_TRUE}))
	raw, err := btc.Encode(tx)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/bitcoin/decode", "/v1/bitcoin/verify-scripts"} {
		body := map[string]any{"data": raw}
		if strings.HasSuffix(path, "verify-scripts") {
			body["prevouts"] = []any{map[string]any{"transactionID": point.Hash.String(), "index": 1, "value": "2", "script": []byte{txscript.OP_TRUE}}}
		}
		w := call(t, s.Handler(), path, body)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out struct {
			ID   string
			Data []byte
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.ID != tx.TxHash().String() || !bytes.Equal(raw, out.Data) {
			t.Fatal("wire transaction changed")
		}
	}
	w := call(t, s.Handler(), "/v1/bitcoin/verify-scripts", map[string]any{"data": raw})
	if w.Code != 422 {
		t.Fatalf("missing prevout %d", w.Code)
	}
	key, _ := crypto.ToECDSA(bytes.Repeat([]byte{1}, 32))
	etx, err := types.SignTx(types.NewTx(&types.LegacyTx{Gas: 21000, GasPrice: big.NewInt(1), Value: big.NewInt(1)}), types.LatestSignerForChainID(big.NewInt(1337)), key)
	if err != nil {
		t.Fatal(err)
	}
	eraw, err := eth.Encode(etx)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/ethereum/decode", "/v1/ethereum/sender"} {
		w = call(t, s.Handler(), path, map[string]any{"data": eraw})
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out struct {
			ID     string
			Sender string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.ID != etx.Hash().Hex() {
			t.Fatal("wrong ETH hash")
		}
		if strings.HasSuffix(path, "sender") && out.Sender != crypto.PubkeyToAddress(key.PublicKey).Hex() {
			t.Fatal("wrong sender")
		}
	}
}

func TestHTTPBoundaries(t *testing.T) {
	btc, _ := bitcoin.New(bitcoin.Config{})
	s, _ := service.New(service.Config{MaxRequestBytes: 128}, service.Deps{Bitcoin: btc})
	for _, tc := range []struct {
		body string
		code int
	}{{`{"data":"AA==","unknown":1}`, 400}, {`{"data":"AA=="} {}`, 400}, {strings.Repeat("x", 129), 413}, {`{"data":"AA=="}`, 400}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/bitcoin/decode", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("%s: got %d want %d", tc.body, w.Code, tc.code)
		}
	}
	if w := call(t, s.Handler(), "/v1/ethereum/decode", map[string]any{}); w.Code != 404 {
		t.Fatal("disabled service route present")
	}
	if _, err := service.New(service.Config{}, service.Deps{}); err == nil {
		t.Fatal("empty service accepted")
	}
	if _, err := service.New(service.Config{MaxConcurrent: -1}, service.Deps{Bitcoin: btc}); err == nil {
		t.Fatal("negative concurrency accepted")
	}
}

func TestRejectTypedNilDependencies(t *testing.T) {
	var btc *bitcoin.Transactions
	var eth *ethereum.Transactions
	valid, _ := bitcoin.New(bitcoin.Config{})
	for _, deps := range []service.Deps{{Bitcoin: btc}, {Ethereum: eth}, {Bitcoin: valid, Ethereum: eth}} {
		if _, err := service.New(service.Config{}, deps); err == nil {
			t.Fatal("typed nil dependency accepted")
		}
	}
}

func TestExpiredRequest(t *testing.T) {
	btc, _ := bitcoin.New(bitcoin.Config{})
	s, _ := service.New(service.Config{}, service.Deps{Bitcoin: btc})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/bitcoin/decode", strings.NewReader(`{"data":"AA=="}`)).WithContext(ctx)
	s.Handler().ServeHTTP(w, r)
	if w.Code != 504 {
		t.Fatalf("deadline status: got %d want 504", w.Code)
	}
}

func TestPluginAssemblyAndLifecycle(t *testing.T) {
	cfg := `root: tools
plugins:
  btc:
    use: transactions/bitcoin
  eth:
    use: transactions/ethereum
    config: {chainID: "1337"}
  tools:
    use: service/transactions
    config: {listen: "127.0.0.1:0"}
    deps: {bitcoin: btc, ethereum: eth}
`
	root, _, err := assembly.Load(context.Background(), strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := root.(*service.Server)
	if !ok {
		t.Fatal("unexpected root")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- root.Run(ctx) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(s.Addresses()) == 0 {
		select {
		case err := <-done:
			t.Fatalf("early exit %v", err)
		case <-deadline.C:
			t.Fatal("listener did not start")
		case <-tick.C:
		}
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + s.Addresses()[0] + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("health %s", body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if err := root.Run(context.Background()); err == nil {
		t.Fatal("runtime reused")
	}
}
