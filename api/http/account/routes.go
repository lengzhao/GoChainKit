package account

import (
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	wire "github.com/lengzhao/GoChainKit/api/http"
	assetcap "github.com/lengzhao/GoChainKit/cap/asset"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	modelcap "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	domain "github.com/lengzhao/GoChainKit/model/account"
	"net/http"
	"reflect"
)

type Config struct{}
type Deps struct {
	Protocol modelcap.Provider `json:"protocol"`
	Query    *api.Service      `json:"query"`
}
type Routes struct{ query *api.Service }

func New(_ Config, d Deps) (*Routes, error) {
	if d.Protocol == nil || (reflect.ValueOf(d.Protocol).Kind() == reflect.Ptr && reflect.ValueOf(d.Protocol).IsNil()) || d.Query == nil {
		return nil, fmt.Errorf("account API protocol and query required")
	}
	if err := core.RequireCompatible(d.Query.Protocol(), d.Protocol); err != nil {
		return nil, err
	}
	engine, ok := d.Protocol.ExecutionModel().(*domain.Engine)
	if !ok || engine == nil {
		return nil, fmt.Errorf("account API requires active account engine")
	}
	return &Routes{d.Query}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.query.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.query.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{{Pattern: "GET /v1/accounts/{address}", Handler: http.HandlerFunc(r.read)}}
}
func (r *Routes) read(w http.ResponseWriter, req *http.Request) {
	id, err := wire.ParseID(req.PathValue("address"), 32)
	if err != nil || len(id) != 32 {
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
	var address assetcap.Address
	copy(address[:], []byte(id))
	value, readErr := domain.ReadAccount(req.Context(), s, address)
	if err = errors.Join(readErr, s.Close()); err != nil {
		wire.WriteError(w, err)
		return
	}
	wire.WriteJSON(w, 200, struct {
		At      string `json:"at"`
		Address string `json:"address"`
		Balance uint64 `json:"balance,string"`
		Nonce   uint64 `json:"nonce,string"`
	}{wire.Hex(anchor), wire.Hex(id), value.Balance, value.Nonce})
}

var _ httpcap.RouteProvider = (*Routes)(nil)
