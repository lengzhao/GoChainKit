package utxo

import (
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	wire "github.com/lengzhao/GoChainKit/api/http"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	modelcap "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	domain "github.com/lengzhao/GoChainKit/model/utxo"
	"net/http"
	"reflect"
	"strconv"
)

type Config struct{}
type Deps struct {
	Protocol modelcap.Provider `json:"protocol"`
	Query    *api.Service      `json:"query"`
}
type Routes struct{ query *api.Service }

func New(_ Config, d Deps) (*Routes, error) {
	if d.Protocol == nil || (reflect.ValueOf(d.Protocol).Kind() == reflect.Ptr && reflect.ValueOf(d.Protocol).IsNil()) || d.Query == nil {
		return nil, fmt.Errorf("utxo API protocol and query required")
	}
	if err := core.RequireCompatible(d.Query.Protocol(), d.Protocol); err != nil {
		return nil, err
	}
	engine, ok := d.Protocol.ExecutionModel().(*domain.Engine)
	if !ok || engine == nil {
		return nil, fmt.Errorf("utxo API requires active utxo engine")
	}
	return &Routes{d.Query}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.query.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.query.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{{Pattern: "GET /v1/utxos/{transaction}/{index}", Handler: http.HandlerFunc(r.read)}}
}
func (r *Routes) read(w http.ResponseWriter, req *http.Request) {
	id, err := wire.ParseID(req.PathValue("transaction"), 32)
	if err != nil || len(id) != 32 {
		wire.WriteError(w, api.ErrInvalidInput)
		return
	}
	index, err := strconv.ParseUint(req.PathValue("index"), 10, 32)
	if err != nil {
		wire.WriteError(w, api.ErrInvalidInput)
		return
	}
	at, err := wire.At(req, 4096)
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	anchor, s, err := r.query.Snapshot(req.Context(), at)
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	point := domain.Outpoint{Index: uint32(index)}
	copy(point.TransactionID[:], []byte(id))
	value, readErr := domain.Lookup(req.Context(), s, point)
	if err = errors.Join(readErr, s.Close()); err != nil {
		wire.WriteError(w, err)
		return
	}
	wire.WriteJSON(w, 200, struct {
		At            string `json:"at"`
		TransactionID string `json:"transactionId"`
		Index         uint64 `json:"index,string"`
		Owner         string `json:"owner"`
		Amount        uint64 `json:"amount,string"`
	}{wire.Hex(anchor), wire.Hex(id), index, wire.Hex(core.ID(value.To[:])), value.Amount})
}

var _ httpcap.RouteProvider = (*Routes)(nil)
