// Package evm exposes optional state queries for its native execution model.
package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/lengzhao/GoChainKit/api"
	transport "github.com/lengzhao/GoChainKit/api/http"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/model/evm"
	"io"
	"net/http"
	"reflect"
	"strings"
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
	query    *api.Service
	engine   *evm.Engine
	contexts model.QueryContextProvider
	codec    core.BlockCodec
}

func New(_ Config, d Deps) (*Routes, error) {
	if err := validate(d); err != nil {
		return nil, err
	}
	e, ok := d.Protocol.ExecutionModel().(*evm.Engine)
	if !ok || e == nil {
		return nil, fmt.Errorf("native EVM API requires active EVM model")
	}
	contexts, ok := d.Protocol.(model.QueryContextProvider)
	if !ok {
		return nil, fmt.Errorf("native EVM API requires anchored query context")
	}
	codec, ok := d.Protocol.(core.BlockCodec)
	if !ok {
		return nil, fmt.Errorf("native EVM API requires block codec")
	}
	return &Routes{d.Query, e, contexts, codec}, nil
}
func (r *Routes) Protocol() core.ProtocolDescriptor { return r.query.Protocol() }
func (r *Routes) LedgerID() *core.LedgerIdentity    { return r.query.LedgerID() }
func (r *Routes) Routes() []httpcap.Route {
	return []httpcap.Route{
		{Pattern: "GET /v1/evm/accounts/{address}", Handler: http.HandlerFunc(r.account)},
		{Pattern: "GET /v1/evm/storage/{address}/{key}", Handler: http.HandlerFunc(r.storage)},
		{Pattern: "POST /v1/evm/call", Handler: http.HandlerFunc(r.call)},
	}
}
func (r *Routes) account(w http.ResponseWriter, req *http.Request) {
	if !common.IsHexAddress(req.PathValue("address")) {
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
	value, readErr := r.engine.Account(req.Context(), s, common.HexToAddress(req.PathValue("address")))
	if err = errors.Join(readErr, s.Close()); err != nil {
		transport.WriteError(w, err)
		return
	}
	transport.WriteJSON(w, 200, struct {
		At      string `json:"at"`
		Balance string `json:"balance"`
		Nonce   uint64 `json:"nonce,string"`
		Code    []byte `json:"code"`
	}{transport.Hex(anchor), value.Balance, value.Nonce, value.Code})
}
func (r *Routes) storage(w http.ResponseWriter, req *http.Request) {
	key, err := hex.DecodeString(strings.TrimPrefix(req.PathValue("key"), "0x"))
	if err != nil || len(key) != 32 || !common.IsHexAddress(req.PathValue("address")) {
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
	value, readErr := r.engine.Storage(req.Context(), s, common.HexToAddress(req.PathValue("address")), common.BytesToHash(key))
	if err = errors.Join(readErr, s.Close()); err != nil {
		transport.WriteError(w, err)
		return
	}
	transport.WriteJSON(w, 200, struct {
		At    string `json:"at"`
		Value string `json:"value"`
	}{transport.Hex(anchor), value.Hex()})
}

type callInput struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Data  []byte `json:"data"`
	Value string `json:"value"`
	Gas   uint64 `json:"gas,string"`
}

func (r *Routes) call(w http.ResponseWriter, req *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 2<<20))
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			transport.WriteError(w, api.ErrTooLarge)
		} else {
			transport.WriteError(w, api.ErrInvalidInput)
		}
		return
	}
	var input callInput
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF || !common.IsHexAddress(input.To) || input.From != "" && !common.IsHexAddress(input.From) {
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
	execution, err := r.contexts.QueryContext(req.Context(), apiHistory{r.query, r.codec, anchor}, anchor)
	if err != nil {
		transport.WriteError(w, errors.Join(err, s.Close()))
		return
	}
	result, callErr := r.engine.CallInContext(req.Context(), s, evm.CallRequest{From: common.HexToAddress(input.From), To: common.HexToAddress(input.To), Data: input.Data, Value: input.Value, Gas: input.Gas}, execution)
	closeErr := s.Close()
	err = errors.Join(callErr, closeErr)
	if closeErr != nil {
		transport.WriteError(w, err)
		return
	}
	status := 200
	if err != nil {
		if errors.Is(err, evm.ErrCallInput) {
			transport.WriteError(w, api.ErrInvalidInput)
			return
		}
		if errors.Is(err, evm.ErrCallExecution) {
			status = 422
		} else {
			transport.WriteError(w, err)
			return
		}
	}
	transport.WriteJSON(w, status, struct {
		At       string `json:"at"`
		Data     []byte `json:"data"`
		GasUsed  uint64 `json:"gasUsed,string"`
		Reverted bool   `json:"reverted"`
	}{transport.Hex(anchor), result.Data, result.GasUsed, result.Reverted})
}

type apiHistory struct {
	query  *api.Service
	codec  core.BlockCodec
	anchor core.ID
}

func (h apiHistory) Protocol() core.ProtocolDescriptor         { return h.query.Protocol() }
func (h apiHistory) Head(ctx context.Context) (core.ID, error) { return h.anchor, ctx.Err() }
func (h apiHistory) Block(ctx context.Context, id core.ID) (core.Block, error) {
	view, err := h.query.Block(ctx, id)
	if err != nil {
		return nil, err
	}
	return h.codec.DecodeBlock(view.Data)
}

var _ httpcap.RouteProvider = (*Routes)(nil)
