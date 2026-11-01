package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/lengzhao/GoChainKit/api"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"github.com/lengzhao/GoChainKit/plugins/mempool"
	"github.com/lengzhao/GoChainKit/plugins/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func service(t *testing.T) *api.Service {
	t.Helper()
	p := testprotocol.New("http")
	l, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 100, MaxBytes: 2 << 20}, mempool.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	s, e := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: l, Pool: pool})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestWireAndRoutes(t *testing.T) {
	s, e := New(Config{}, Deps{Query: service(t)})
	if e != nil {
		t.Fatal(e)
	}
	for _, tt := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/v1/chain/head", "", 200}, {"GET", "/v1/transactions/ff00", "", 404}, {"GET", "/v1/blocks/zz", "", 400}, {"GET", "/missing", "", 404}, {"POST", "/v1/node", "", 405}, {"POST", "/v1/transactions", `{"data":"","extra":1}`, 400}, {"POST", "/v1/transactions", `{"data":"AA=="} {}`, 400}} {
		r := httptest.NewRecorder()
		s.handler.ServeHTTP(r, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
		if r.Code != tt.status {
			t.Fatalf("%s: %d %s", tt.path, r.Code, r.Body)
		}
		if tt.status == 405 && r.Header().Get("Allow") == "" {
			t.Fatal("missing Allow header")
		}
		var v any
		if json.Unmarshal(r.Body.Bytes(), &v) != nil {
			t.Fatal("non JSON", r.Body)
		}
	}
}
func TestErrorTree(t *testing.T) {
	for _, tt := range []struct {
		e    error
		code int
	}{{core.ErrNotFound, 404}, {errors.Join(core.ErrNotFound, errors.New("secret")), 500}, {errors.Join(context.Canceled, errors.New("secret")), 500}, {&api.InputError{Cause: errors.New("bad")}, 400}} {
		r := httptest.NewRecorder()
		WriteError(r, tt.e)
		if r.Code != tt.code || strings.Contains(r.Body.String(), "secret") {
			t.Fatal(r.Code, r.Body)
		}
	}
}
func TestRealLifecycle(t *testing.T) {
	s, e := New(Config{Listen: "127.0.0.1:0"}, Deps{Query: service(t)})
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Addresses()) != 0 {
		t.Fatal("constructor started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	r, e := http.Get(s.Addresses()[0] + "/v1/node")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = s.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.Start(context.Background()); e == nil {
		t.Fatal("reused")
	}
}
func TestBoundsAndMethods(t *testing.T) {
	s, _ := New(Config{MaxRequestBytes: 20, MaxResponseBytes: 128}, Deps{Query: service(t)})
	for _, tt := range []struct {
		method, path, body string
		code               int
	}{{"GET", "/v1/transactions", "", 405}, {"POST", "/v1/transactions", `{"data":"AA=="}                      `, 413}, {"GET", "/v1/chain/head", "", 500}} {
		w := httptest.NewRecorder()
		s.handler.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
		if w.Code != tt.code {
			t.Fatalf("%s %d %s", tt.path, w.Code, w.Body)
		}
	}
}

type module struct {
	s      *api.Service
	routes []httpcap.Route
}

func (m module) Protocol() core.ProtocolDescriptor { return m.s.Protocol() }
func (m module) LedgerID() *core.LedgerIdentity    { return m.s.LedgerID() }
func (m module) Routes() []httpcap.Route           { return m.routes }
func TestModuleLimitsAndCloseJoin(t *testing.T) {
	q := service(t)
	entered := make(chan struct{})
	exited := make(chan struct{})
	m := module{q, []httpcap.Route{{Pattern: "GET /slow", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		defer close(exited)
		WriteError(w, errors.Join(r.Context().Err(), errors.New("secret")))
	})}}}
	s, e := New(Config{Listen: "127.0.0.1:0", MaxConcurrent: 1}, Deps{Query: q, Modules: []httpcap.RouteProvider{m}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		r, e := http.Get(s.Addresses()[0] + "/slow")
		if e == nil {
			r.Body.Close()
		}
	}()
	<-entered
	r, e := http.Get(s.Addresses()[0] + "/v1/node")
	if e != nil {
		t.Fatal(e)
	}
	if r.StatusCode != 429 {
		t.Fatal(r.StatusCode)
	}
	r.Body.Close()
	if e = s.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-exited:
	default:
		t.Fatal("handler not joined")
	}
	<-requestDone
	if _, e = New(Config{}, Deps{Query: q, Modules: []httpcap.RouteProvider{module{q, []httpcap.Route{{Pattern: "GET /v1/node", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}}}}); e == nil {
		t.Fatal("duplicate route accepted")
	}
}
func TestRequestDeadlinePreservesMixedFault(t *testing.T) {
	q := service(t)
	m := module{q, []httpcap.Route{{Pattern: "GET /slow", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		WriteError(w, errors.Join(r.Context().Err(), errors.New("secret")))
	})}}}
	s, e := New(Config{RequestTimeout: "1ms"}, Deps{Query: q, Modules: []httpcap.RouteProvider{m}})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("GET", "/slow", nil))
	if w.Code != 500 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestAdmissionAndOpaqueIDs(t *testing.T) {
	q := service(t)
	s, _ := New(Config{}, Deps{Query: q})
	p := testprotocol.New("http")
	tx := testprotocol.Tx{Label: "binary", Body: []byte{255, 0, 128}}
	raw, e := p.EncodeTransaction(tx)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string][]byte{"data": raw})
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/transactions", bytes.NewReader(body)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	var a struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &a)
	if a.ID != Hex(tx.ID()) || a.Status != "accepted" {
		t.Fatal(a)
	}
	w = httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/transactions/"+a.ID, nil))
	var got struct {
		Data   []byte `json:"data"`
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || !bytes.Equal(raw, got.Data) || got.Status != "pending" {
		t.Fatal(w.Code, w.Body)
	}
	id := core.ID(string([]byte{255, 0, 128}))
	if decoded, e := ParseID(Hex(id), 3); e != nil || decoded != id {
		t.Fatal(decoded, e)
	}
	if _, e := ParseID(Hex(id), 2); e == nil {
		t.Fatal("ID budget bypass")
	}
}
func TestAdmissionRequiresBase64String(t *testing.T) {
	s, _ := New(Config{}, Deps{Query: service(t)})
	for _, body := range []string{`{"data":[1,2,3]}`, `{"data":null}`, `{}`, `{"data":""}`, `{"data":"%%%"}`} {
		w := httptest.NewRecorder()
		s.handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/transactions", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body)
		}
	}
}

func TestAdmissionRejectsNumericByteArray(t *testing.T) {
	p := testprotocol.New("http")
	raw, _ := p.EncodeTransaction(testprotocol.Tx{Label: "array"})
	numbers := make([]int, len(raw))
	for i, b := range raw {
		numbers[i] = int(b)
	}
	body, _ := json.Marshal(map[string]any{"data": numbers})
	s, _ := New(Config{}, Deps{Query: service(t)})
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1/transactions", bytes.NewReader(body)))
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestAtRejectsMalformedQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/accounts/a?at=%zz", nil)
	if _, e := At(r, 4096); e == nil {
		t.Fatal("malformed anchor accepted")
	}
}

func TestEmptyReceiptIsBase64String(t *testing.T) {
	ctx := context.Background()
	p := testprotocol.New("empty-receipt")
	ledger, e := storage.NewMemory(storage.MemoryConfig{}, storage.Deps{Protocol: p})
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := mempool.NewMemory(mempool.MemoryConfig{MaxTransactions: 10, MaxBytes: 4096}, mempool.Deps{Protocol: p})
	q, _ := api.New(api.Config{}, api.Deps{Protocol: p, Ledger: ledger, Pool: pool})
	anchor, snap, e := q.Snapshot(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	parent, _ := ledger.Block(ctx, anchor)
	tx := testprotocol.Tx{Label: "empty"}
	draft, e := p.Draft(ctx, parent, []core.Transaction{tx})
	if e != nil {
		t.Fatal(e)
	}
	execution, e := (testprotocol.Executor{P: p}).Execute(ctx, snap, draft)
	if e != nil {
		t.Fatal(e)
	}
	execution.Receipts = []core.Receipt{{TransactionID: tx.ID()}}
	block, e := p.Finalize(ctx, draft, execution)
	if e != nil {
		t.Fatal(e)
	}
	execution.BlockID = block.ID()
	if e = ledger.Commit(ctx, anchor, block, execution); e != nil {
		t.Fatal(e)
	}
	s, _ := New(Config{}, Deps{Query: q})
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/receipts/"+Hex(tx.ID()), nil))
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["data"] != "" {
		t.Fatal(w.Code, w.Body)
	}
}
