// Package height contributes optional anchored-height HTTP queries.
package height

import (
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	wire "github.com/lengzhao/GoChainKit/api/http"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	querycap "github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
	"net/http"
	"strconv"
)

type BoundIndex interface {
	querycap.HeightIndex
	core.LedgerBound
}
type Protocol interface {
	core.ProtocolBound
	core.BlockCodec
}
type Config struct{}
type Deps struct {
	Protocol Protocol   `json:"protocol"`
	Index    BoundIndex `json:"index"`
}
type Routes struct{ d Deps }

func New(_ Config, d Deps) (*Routes, error) {
	if d.Protocol == nil || d.Index == nil || d.Index.LedgerID() == nil {
		return nil, fmt.Errorf("height API requires protocol and index")
	}
	if e := core.RequireCompatible(d.Protocol.Protocol(), d.Index); e != nil {
		return nil, e
	}
	return &Routes{d}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.d.Protocol.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.d.Index.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{{Pattern: "GET /v1/chain/heights/{height}", Handler: http.HandlerFunc(r.read)}}
}
func (r *Routes) read(w http.ResponseWriter, req *http.Request) {
	raw := req.PathValue("height")
	height, e := strconv.ParseUint(raw, 10, 64)
	if e != nil || strconv.FormatUint(height, 10) != raw {
		wire.WriteError(w, api.ErrInvalidInput)
		return
	}
	anchor, e := wire.At(req, 4096)
	if e != nil || anchor == "" {
		wire.WriteError(w, api.ErrInvalidInput)
		return
	}
	b, e := r.d.Index.AtHeight(req.Context(), anchor, height)
	if e != nil {
		wire.WriteError(w, e)
		return
	}
	data, e := r.d.Protocol.EncodeBlock(b)
	if e != nil {
		wire.WriteError(w, e)
		return
	}
	wire.WriteJSON(w, 200, struct {
		At     string `json:"at"`
		Height uint64 `json:"height,string"`
		ID     string `json:"id"`
		Data   []byte `json:"data"`
	}{wire.Hex(anchor), height, wire.Hex(b.ID()), data})
}
