package http

import (
	"bytes"
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
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"
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
	cfg                             Config
	deps                            Deps
	handler                         http.Handler
	server                          *http.Server
	mu                              sync.Mutex
	used                            bool
	listener                        net.Listener
	done                            chan struct{}
	serveErr                        error
	requestTimeout, shutdownTimeout time.Duration
	slots                           chan struct{}
	active                          sync.WaitGroup
	stopping                        bool
	cancel                          context.CancelFunc
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
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = (2 << 20) + 1024
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 8 << 20
	}
	if c.MaxIDBytes == 0 {
		c.MaxIDBytes = 4096
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 64
	}
	if c.MaxRequestBytes < 1 || c.MaxResponseBytes < 128 || c.MaxIDBytes < 1 || c.MaxConcurrent < 1 {
		return nil, fmt.Errorf("HTTP limits must be positive (response >=128)")
	}
	s := &Server{cfg: c, deps: d, done: make(chan struct{}), slots: make(chan struct{}, c.MaxConcurrent)}
	dur := func(raw string, def time.Duration) (time.Duration, error) {
		if raw == "" {
			return def, nil
		}
		v, e := time.ParseDuration(raw)
		if e != nil || v <= 0 {
			return 0, fmt.Errorf("positive HTTP timeout required")
		}
		return v, nil
	}
	s.requestTimeout, err = dur(c.RequestTimeout, 10*time.Second)
	if err != nil {
		return nil, err
	}
	s.shutdownTimeout, err = dur(c.ShutdownTimeout, 5*time.Second)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{}
	for _, v := range []struct {
		raw string
		def time.Duration
		dst *time.Duration
	}{{c.ReadHeaderTimeout, 5 * time.Second, &srv.ReadHeaderTimeout}, {c.ReadTimeout, 15 * time.Second, &srv.ReadTimeout}, {c.WriteTimeout, 15 * time.Second, &srv.WriteTimeout}, {c.IdleTimeout, 30 * time.Second, &srv.IdleTimeout}} {
		*v.dst, err = dur(v.raw, v.def)
		if err != nil {
			return nil, err
		}
	}
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
	s.handler = s.bound(mux)
	srv.Handler = s.handler
	s.server = srv
	return s, nil
}
func (s *Server) Protocol() core.ProtocolDescriptor { return s.deps.Query.Protocol() }
func (s *Server) LedgerID() *core.LedgerIdentity    { return s.deps.Query.LedgerID() }
func (s *Server) Addresses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return []string{"http://" + s.listener.Addr().String()}
}
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used {
		return fmt.Errorf("HTTP server is single use")
	}
	s.used = true
	l, e := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.Listen)
	if e != nil {
		return e
	}
	s.listener = l
	base, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.server.BaseContext = func(net.Listener) context.Context { return base }
	go func() {
		e := s.server.Serve(l)
		s.mu.Lock()
		if !errors.Is(e, http.ErrServerClosed) {
			s.serveErr = e
		}
		s.mu.Unlock()
		close(s.done)
	}()
	return nil
}
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	started := s.listener != nil
	s.mu.Unlock()
	if !started {
		return fmt.Errorf("HTTP server not started")
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		s.mu.Lock()
		s.cancel()
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serveErr
}
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	started := s.listener != nil
	s.stopping = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	if !started {
		return nil
	}
	stop, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()
	e := s.server.Shutdown(stop)
	if e != nil {
		e = errors.Join(e, s.server.Close())
	}
	<-s.done
	s.active.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(e, s.serveErr)
}

type boundedResponse struct {
	header      http.Header
	body        bytes.Buffer
	status, max int
	err         error
}

func (b *boundedResponse) Header() http.Header { return b.header }
func (b *boundedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}
func (b *boundedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	if len(p) > b.max-b.body.Len() {
		b.err = errResponseLimit
		return 0, b.err
	}
	return b.body.Write(p)
}
func (s *Server) bound(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			writeError(w, 503, "closed")
			return
		}
		s.active.Add(1)
		s.mu.Unlock()
		defer s.active.Done()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			writeError(w, 429, "capacity")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes)
		b := &boundedResponse{header: make(http.Header), max: s.cfg.MaxResponseBytes}
		mux.ServeHTTP(b, r)
		if b.status == 404 || b.status == 405 {
			allow := b.header.Get("Allow")
			b.body.Reset()
			b.header = make(http.Header)
			if allow != "" {
				b.header.Set("Allow", allow)
			}
			status := b.status
			b.status = 0
			code := "not_found"
			if status == 405 {
				code = "method_not_allowed"
			}
			writeError(b, status, code)
		}
		if b.err != nil {
			WriteError(w, b.err)
			return
		}
		if ctx.Err() != nil && b.status < 400 {
			WriteError(w, ctx.Err())
			return
		}
		for k, v := range b.header {
			w.Header()[k] = v
		}
		if b.status == 0 {
			b.status = 200
		}
		w.WriteHeader(b.status)
		_, _ = w.Write(b.body.Bytes())
	})
}
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
