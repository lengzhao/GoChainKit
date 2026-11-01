package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lengzhao/GoChainKit/api"
	httpcap "github.com/lengzhao/GoChainKit/cap/http"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/cap/query"
	"github.com/lengzhao/GoChainKit/core"
	host "github.com/lengzhao/GoChainKit/runtime/http"
	"io"
	"net/http"
	"reflect"
)

type Config struct {
	Listen            string `json:"listen"`
	MaxRequestBytes   int64  `json:"maxRequestBytes"`
	MaxResponseBytes  int    `json:"maxResponseBytes"`
	MaxIDBytes        int    `json:"maxIDBytes"`
	MaxConcurrent     int    `json:"maxConcurrent"`
	ReadHeaderTimeout string `json:"readHeaderTimeout"`
	ReadTimeout       string `json:"readTimeout"`
	WriteTimeout      string `json:"writeTimeout"`
	IdleTimeout       string `json:"idleTimeout"`
	RequestTimeout    string `json:"requestTimeout"`
	ShutdownTimeout   string `json:"shutdownTimeout"`
}
type Deps struct {
	Query   *api.Service            `json:"query"`
	Network networkcap.Network      `json:"network,omitempty"`
	Modules []httpcap.RouteProvider `json:"modules,omitempty"`
}
type Server struct {
	cfg     Config
	deps    Deps
	handler http.Handler
	host    *host.Server
}

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	return r.Kind() == reflect.Ptr && r.IsNil()
}
func New(c Config, d Deps) (out *Server, err error) {
	if d.Query == nil {
		return nil, fmt.Errorf("HTTP query service required")
	}
	if c.MaxIDBytes == 0 {
		c.MaxIDBytes = 4096
	}
	if c.MaxIDBytes < 1 {
		return nil, fmt.Errorf("positive HTTP ID limit required")
	}
	s := &Server{cfg: c, deps: d}
	if !nilValue(d.Network) {
		if err = core.RequireCompatible(s.Protocol(), d.Network); err != nil {
			return nil, err
		}
	} else {
		s.deps.Network = nil
	}
	mux := http.NewServeMux()
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = fmt.Errorf("invalid or conflicting HTTP routes: %v", r)
		}
	}()
	for _, route := range s.routes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	for _, m := range d.Modules {
		if nilValue(m) {
			return nil, fmt.Errorf("nil HTTP route provider")
		}
		if err = core.RequireCompatible(s.Protocol(), m); err != nil {
			return nil, err
		}
		if m.LedgerID() != s.LedgerID() {
			return nil, core.ErrLedgerMismatch
		}
		for _, route := range m.Routes() {
			mux.Handle(route.Pattern, route.Handler)
		}
	}
	h, err := host.New(host.Config{
		Listen: c.Listen, MaxRequestBytes: c.MaxRequestBytes, MaxResponseBytes: c.MaxResponseBytes, MaxConcurrent: c.MaxConcurrent,
		ReadHeaderTimeout: c.ReadHeaderTimeout, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, IdleTimeout: c.IdleTimeout,
		RequestTimeout: c.RequestTimeout, ShutdownTimeout: c.ShutdownTimeout,
	}, mux, host.Errors{Status: writeError, Failure: WriteError, NormalizeMuxErrors: true})
	if err != nil {
		return nil, err
	}
	s.host = h
	s.handler = h.Handler()
	return s, nil
}
func (s *Server) Protocol() core.ProtocolDescriptor { return s.deps.Query.Protocol() }
func (s *Server) LedgerID() *core.LedgerIdentity    { return s.deps.Query.LedgerID() }
func (s *Server) Handler() http.Handler             { return s.handler }
func (s *Server) Addresses() []string               { return s.host.Addresses() }
func (s *Server) Start(ctx context.Context) error   { return s.host.Start(ctx) }
func (s *Server) Run(ctx context.Context) error     { return s.host.Run(ctx) }
func (s *Server) Stop(ctx context.Context) error    { return s.host.Stop(ctx) }

func (s *Server) routes() []httpcap.Route {
	wrap := func(pattern string, f http.HandlerFunc) httpcap.Route {
		return httpcap.Route{Pattern: pattern, Handler: f}
	}
	return []httpcap.Route{
		wrap("POST /v1/transactions", func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Data string `json:"data"`
			}
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if e := dec.Decode(&in); e != nil {
				var large *http.MaxBytesError
				if errors.As(e, &large) {
					writeError(w, 413, "too_large")
				} else {
					WriteError(w, api.ErrInvalidInput)
				}
				return
			}
			if e := dec.Decode(new(any)); e != io.EOF || len(in.Data) == 0 {
				var large *http.MaxBytesError
				if errors.As(e, &large) {
					writeError(w, 413, "too_large")
				} else {
					WriteError(w, api.ErrInvalidInput)
				}
				return
			}
			raw, e := base64.StdEncoding.DecodeString(in.Data)
			if e != nil {
				WriteError(w, api.ErrInvalidInput)
				return
			}
			a, e := s.deps.Query.SubmitEncoded(r.Context(), raw)
			if e != nil {
				WriteError(w, e)
				return
			}
			status := 202
			if a.Status == query.Confirmed {
				status = 200
			}
			WriteJSON(w, status, map[string]any{"id": Hex(a.TransactionID), "status": a.Status, "blockId": Hex(a.BlockID), "head": Hex(a.Head)})
		}),
		wrap("GET /v1/chain/head", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.deps.Query.Head(r.Context())
			if e != nil {
				WriteError(w, e)
				return
			}
			WriteJSON(w, 200, map[string]any{"id": Hex(v.Block.ID), "data": v.Block.Data, "stateRoot": Hex(core.ID(v.StateRoot))})
		}),
		wrap("GET /v1/blocks/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, e := ParseID(r.PathValue("id"), s.cfg.MaxIDBytes)
			if e != nil {
				WriteError(w, e)
				return
			}
			v, e := s.deps.Query.Block(r.Context(), id)
			if e != nil {
				WriteError(w, e)
				return
			}
			WriteJSON(w, 200, map[string]any{"id": Hex(v.ID), "data": v.Data})
		}),
		wrap("GET /v1/transactions/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, e := ParseID(r.PathValue("id"), s.cfg.MaxIDBytes)
			if e != nil {
				WriteError(w, e)
				return
			}
			v, e := s.deps.Query.Transaction(r.Context(), id)
			if e != nil {
				WriteError(w, e)
				return
			}
			status := string(v.Status)
			if v.Status == query.Accepted {
				status = "pending"
			}
			WriteJSON(w, 200, struct {
				ID      string `json:"id"`
				Data    []byte `json:"data"`
				Status  string `json:"status"`
				BlockID string `json:"blockId,omitempty"`
				Index   uint64 `json:"index,string"`
				Head    string `json:"head,omitempty"`
			}{Hex(v.ID), v.Data, status, Hex(v.BlockID), v.Index, Hex(v.Head)})
		}),
		wrap("GET /v1/receipts/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, e := ParseID(r.PathValue("id"), s.cfg.MaxIDBytes)
			if e != nil {
				WriteError(w, e)
				return
			}
			v, e := s.deps.Query.Receipt(r.Context(), id)
			if e == nil && v.Receipt == nil {
				e = core.ErrNotFound
			}
			if e != nil {
				WriteError(w, e)
				return
			}
			WriteJSON(w, 200, struct {
				ID      string `json:"id"`
				Data    []byte `json:"data"`
				BlockID string `json:"blockId"`
				Index   uint64 `json:"index,string"`
				Head    string `json:"head"`
			}{Hex(v.TransactionID), append([]byte{}, v.Receipt.Data...), Hex(v.BlockID), v.Index, Hex(v.Head)})
		}),
		wrap("GET /v1/node", func(w http.ResponseWriter, r *http.Request) {
			p := s.Protocol()
			out := map[string]any{"protocol": map[string]string{"name": p.Name, "version": p.Version, "network": p.Network}}
			if s.deps.Network != nil {
				out["network"] = map[string]any{"addresses": s.deps.Network.Addresses(), "peers": s.deps.Network.Peers()}
			}
			WriteJSON(w, 200, out)
		})}
}

var _ httpcap.Server = (*Server)(nil)
