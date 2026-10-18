package account_test

import (
	"context"
	"errors"
	"github.com/lengzhao/GoChainKit/api"
	wire "github.com/lengzhao/GoChainKit/api/http"
	accountapi "github.com/lengzhao/GoChainKit/api/http/account"
	utxoapi "github.com/lengzhao/GoChainKit/api/http/utxo"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	modelcap "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/model/account"
	"github.com/lengzhao/GoChainKit/model/utxo"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"github.com/lengzhao/GoChainKit/protocol/composed"
	"github.com/lengzhao/GoChainKit/rules/pow"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setup(t *testing.T, name string) (modelcap.Provider, *api.Service) {
	t.Helper()
	var model modelcap.Engine
	var e error
	if name == "account" {
		model, e = account.New(account.Config{Network: "api"})
	} else {
		model, e = utxo.New(utxo.Config{Network: "api"})
	}
	if e != nil {
		t.Fatal(e)
	}
	rule, e := pow.New(pow.Config{})
	if e != nil {
		t.Fatal(e)
	}
	p, e := composed.NewPoW(composed.Config{Network: "api", GenesisTime: 1700000000, Producer: make([]byte, 32)}, model, rule, rule)
	if e != nil {
		t.Fatal(e)
	}
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	query, e := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ledger, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	return p, query
}
func TestModelRouteConstructionAndValidation(t *testing.T) {
	accountProtocol, accountQuery := setup(t, "account")
	utxoProtocol, utxoQuery := setup(t, "utxo")
	if _, e := accountapi.New(accountapi.Config{}, accountapi.Deps{}); e == nil {
		t.Fatal("missing deps accepted")
	}
	var typednil *composed.WorkProtocol
	if _, e := accountapi.New(accountapi.Config{}, accountapi.Deps{Protocol: typednil, Query: accountQuery}); e == nil {
		t.Fatal("nil provider accepted")
	}
	if _, e := accountapi.New(accountapi.Config{}, accountapi.Deps{Protocol: utxoProtocol, Query: utxoQuery}); e == nil {
		t.Fatal("wrong active model accepted")
	}
	if _, e := utxoapi.New(utxoapi.Config{}, utxoapi.Deps{Protocol: accountProtocol, Query: accountQuery}); e == nil {
		t.Fatal("wrong active model accepted")
	}
	if _, e := accountapi.New(accountapi.Config{}, accountapi.Deps{Protocol: accountProtocol, Query: utxoQuery}); e == nil {
		t.Fatal("wrong descriptor accepted")
	}
	accountRoutes, e := accountapi.New(accountapi.Config{}, accountapi.Deps{Protocol: accountProtocol, Query: accountQuery})
	if e != nil {
		t.Fatal(e)
	}
	utxoRoutes, e := utxoapi.New(utxoapi.Config{}, utxoapi.Deps{Protocol: utxoProtocol, Query: utxoQuery})
	if e != nil {
		t.Fatal(e)
	}
	_, otherQuery := setup(t, "account")
	if _, err := wire.New(wire.Config{}, wire.Deps{Query: otherQuery, Modules: []httpcap.RouteProvider{accountRoutes}}); !errors.Is(err, core.ErrLedgerMismatch) {
		t.Fatal("foreign route owner accepted", err)
	}
	if _, err := wire.New(wire.Config{}, wire.Deps{Query: accountQuery, Modules: []httpcap.RouteProvider{accountRoutes, accountRoutes}}); err == nil {
		t.Fatal("duplicate routes accepted")
	}
	if accountRoutes.LedgerID() != accountQuery.LedgerID() || utxoRoutes.LedgerID() != utxoQuery.LedgerID() {
		t.Fatal("owner lost")
	}
	for _, tt := range []struct {
		routes httpcap.RouteProvider
		path   string
		status int
	}{
		{accountRoutes, "/v1/accounts/" + strings.Repeat("00", 32), 200},
		{accountRoutes, "/v1/accounts/00", 400},
		{accountRoutes, "/v1/accounts/" + strings.Repeat("00", 32) + "?at=zz", 400},
		{accountRoutes, "/v1/accounts/" + strings.Repeat("00", 32) + "?at=00&at=01", 400},
		{utxoRoutes, "/v1/utxos/00/0", 400},
		{utxoRoutes, "/v1/utxos/" + strings.Repeat("00", 32) + "/-1", 400},
		{utxoRoutes, "/v1/utxos/" + strings.Repeat("00", 32) + "/4294967296", 400},
		{utxoRoutes, "/v1/utxos/" + strings.Repeat("00", 32) + "/0", 404},
	} {
		mux := http.NewServeMux()
		for _, route := range tt.routes.Routes() {
			mux.Handle(route.Pattern, route.Handler)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != tt.status {
			t.Fatal(tt.path, w.Code, w.Body)
		}
	}
}

type closingLedger struct{ api.Ledger }
type closingReader struct{ core.StateReader }

func (closingReader) Close() error { return errors.New("secret close failure") }
func (l closingLedger) Snapshot(ctx context.Context, id core.ID) (core.StateReader, error) {
	s, e := l.Ledger.Snapshot(ctx, id)
	if e != nil {
		return nil, e
	}
	return closingReader{s}, nil
}
func TestCloseFaultCannotBeReportedAsMissing(t *testing.T) {
	p := testprotocol.New("http")
	l, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	q, e := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: closingLedger{l}, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	engine, _ := utxo.New(utxo.Config{Network: "http"})
	module, e := utxoapi.New(utxoapi.Config{}, utxoapi.Deps{Protocol: provider{p, engine}, Query: q})
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	for _, r := range module.Routes() {
		mux.Handle(r.Pattern, r.Handler)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/utxos/"+strings.Repeat("00", 32)+"/0", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body)
	}
}

type chainProtocol interface{ core.Protocol }
type provider struct {
	chainProtocol
	engine modelcap.Engine
}

func (p provider) ExecutionModel() modelcap.Engine { return p.engine }
