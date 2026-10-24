package utxo

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	wire "github.com/lengzhao/GoChainKit/api/http"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	modelcap "github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model/asset"
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
type Routes struct {
	query   *api.Service
	engine  *domain.Engine
	heights modelcap.BlockHeights
}

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
	heights, _ := d.Protocol.(modelcap.BlockHeights)
	return &Routes{d.Query, engine, heights}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.query.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.query.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{{Pattern: "GET /v1/utxos/{transaction}/{index}", Handler: http.HandlerFunc(r.read)}, {Pattern: "GET /v1/addresses/{address}/utxos", Handler: http.HandlerFunc(r.address)}}
}

// Balance is anchored to At. Spendable is evaluated for the next block; totals
// cover all coins, while Coins is paged. All amounts/heights are decimal strings.
type Balance struct {
	At         string `json:"at"`
	Address    string `json:"address"`
	Height     uint64 `json:"height,string"`
	Spendable  uint64 `json:"spendable,string"`
	Immature   uint64 `json:"immature,string"`
	Coins      []Coin `json:"coins"`
	NextOffset int    `json:"nextOffset"`
}
type Coin struct {
	TransactionID string `json:"transactionId"`
	Index         uint32 `json:"index,string"`
	Amount        uint64 `json:"amount,string"`
	Coinbase      bool   `json:"coinbase"`
	CreatedHeight uint64 `json:"createdHeight,string"`
	MatureHeight  uint64 `json:"matureHeight,string"`
	Spendable     bool   `json:"spendable"`
}

func (r *Routes) address(w http.ResponseWriter, req *http.Request) {
	if r.heights == nil {
		wire.WriteError(w, fmt.Errorf("address query requires block heights"))
		return
	}
	raw, err := hex.DecodeString(req.PathValue("address"))
	if err != nil || len(raw) != 32 {
		wire.WriteError(w, api.ErrInvalidInput)
		return
	}
	var owner [32]byte
	copy(owner[:], raw)
	limit, offset := 128, 0
	for _, p := range []struct {
		name string
		dst  *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if value := req.URL.Query().Get(p.name); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 {
				wire.WriteError(w, api.ErrInvalidInput)
				return
			}
			*p.dst = n
		}
	}
	if limit < 1 || limit > 256 {
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
	closed := false
	defer func() {
		if !closed {
			_ = s.Close()
		}
	}()
	view, err := r.query.Block(req.Context(), anchor)
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	b, err := r.heights.DecodeBlock(view.Data)
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	height, err := r.heights.BlockHeight(b)
	if err != nil || height == ^uint64(0) {
		wire.WriteError(w, fmt.Errorf("invalid height"))
		return
	}
	coins, err := r.engine.Coins(req.Context(), s, owner, height+1)
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	err = s.Close()
	closed = true
	if err != nil {
		wire.WriteError(w, err)
		return
	}
	out := Balance{At: wire.Hex(anchor), Address: hex.EncodeToString(owner[:]), Height: height, Coins: []Coin{}}
	for i, c := range coins {
		if c.Spendable {
			out.Spendable, err = asset.Add(out.Spendable, c.Amount)
		} else {
			out.Immature, err = asset.Add(out.Immature, c.Amount)
		}
		if err != nil {
			wire.WriteError(w, err)
			return
		}
		if i >= offset && i-offset < limit {
			out.Coins = append(out.Coins, Coin{hex.EncodeToString(c.Outpoint.TransactionID[:]), c.Outpoint.Index, c.Amount, c.Coinbase, c.CreatedHeight, c.MatureHeight, c.Spendable})
		}
	}
	if offset < len(coins) && len(coins)-offset > limit {
		out.NextOffset = offset + limit
	}
	wire.WriteJSON(w, 200, out)
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
