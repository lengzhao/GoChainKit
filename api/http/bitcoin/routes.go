// Package bitcoin exposes optional state queries for its native execution model.
package bitcoin

import (
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/lengzhao/GoChainKit/api"
	transport "github.com/lengzhao/GoChainKit/api/http"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	btc "github.com/lengzhao/GoChainKit/model/bitcoin"
	"net/http"
	"reflect"
	"strconv"
)

type Config struct{}
type Deps struct {
	Protocol model.Provider `json:"protocol"`
	Query    *api.Service   `json:"query"`
}

func validate(d Deps) error {
	if d.Protocol == nil || d.Query == nil {
		return fmt.Errorf("native API: protocol and query required")
	}
	v := reflect.ValueOf(d.Protocol)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return fmt.Errorf("native API: nil protocol")
	}
	return core.RequireCompatible(d.Query.Protocol(), d.Protocol)
}

type Routes struct {
	query  *api.Service
	engine *btc.Engine
}

func New(_ Config, d Deps) (*Routes, error) {
	if err := validate(d); err != nil {
		return nil, err
	}
	e, ok := d.Protocol.ExecutionModel().(*btc.Engine)
	if !ok || e == nil {
		return nil, fmt.Errorf("native bitcoin API requires active bitcoin model")
	}
	return &Routes{d.Query, e}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.query.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.query.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{
		{Pattern: "GET /v1/bitcoin/genesis", Handler: http.HandlerFunc(r.genesis)},
		{Pattern: "GET /v1/bitcoin/utxos/{transaction}/{index}", Handler: http.HandlerFunc(r.output)},
	}
}
func (r *Routes) genesis(w http.ResponseWriter, req *http.Request) {
	type point struct {
		TransactionID string `json:"transactionID"`
		Index         uint32 `json:"index,string"`
	}
	out := []point{}
	for _, p := range r.engine.GenesisOutpoints() {
		out = append(out, point{p.Hash.String(), p.Index})
	}
	transport.WriteJSON(w, 200, struct {
		Outpoints []point `json:"outpoints"`
	}{out})
}
func (r *Routes) output(w http.ResponseWriter, req *http.Request) {
	id, err := chainhash.NewHashFromStr(req.PathValue("transaction"))
	if err != nil || len(req.PathValue("transaction")) != 64 {
		transport.WriteError(w, api.ErrInvalidInput)
		return
	}
	index, err := strconv.ParseUint(req.PathValue("index"), 10, 32)
	if err != nil {
		transport.WriteError(w, api.ErrInvalidInput)
		return
	}
	at, err := transport.At(req, 4096)
	if err != nil {
		transport.WriteError(w, err)
		return
	}
	anchor, s, err := r.query.Snapshot(req.Context(), at)
	if err != nil {
		transport.WriteError(w, err)
		return
	}
	out, readErr := r.engine.Output(req.Context(), s, wire.OutPoint{Hash: *id, Index: uint32(index)})
	if err = errors.Join(readErr, s.Close()); err != nil {
		transport.WriteError(w, err)
		return
	}
	transport.WriteJSON(w, 200, struct {
		At string `json:"at"`
		btc.Output
	}{transport.Hex(anchor), out})
}

var _ httpcap.RouteProvider = (*Routes)(nil)
