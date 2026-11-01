// Package http owns bounded HTTP transport without chain or ledger dependencies.
// Domain hosts supply handlers and error rendering; constructors perform no I/O.
package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	std "net/http"
	"reflect"
	"sync"
	"time"
)

var ErrResponseLimit = errors.New("HTTP response limit exceeded")

type Config struct {
	Listen            string `json:"listen"`
	MaxRequestBytes   int64  `json:"maxRequestBytes"`
	MaxResponseBytes  int    `json:"maxResponseBytes"`
	MaxConcurrent     int    `json:"maxConcurrent"`
	MaxHeaderBytes    int    `json:"maxHeaderBytes"`
	ReadHeaderTimeout string `json:"readHeaderTimeout"`
	ReadTimeout       string `json:"readTimeout"`
	WriteTimeout      string `json:"writeTimeout"`
	IdleTimeout       string `json:"idleTimeout"`
	RequestTimeout    string `json:"requestTimeout"`
	ShutdownTimeout   string `json:"shutdownTimeout"`
}

// Errors renders domain-specific wire errors. Nil callbacks use plain HTTP
// errors. NormalizeMuxErrors optionally replaces ServeMux 404/405 responses.
type Errors struct {
	Status             func(std.ResponseWriter, int, string)
	Failure            func(std.ResponseWriter, error)
	NormalizeMuxErrors bool
}

// Server is single-use. Start/Run/Stop support resource-owning coordinators;
// Serve owns all three steps for standalone use. Stop joins admitted handlers.
type Server struct {
	cfg                             Config
	policy                          Errors
	handler                         std.Handler
	server                          *std.Server
	mu                              sync.Mutex
	used, stopping                  bool
	listener                        net.Listener
	done                            chan struct{}
	serveErr                        error
	requestTimeout, shutdownTimeout time.Duration
	slots                           chan struct{}
	active                          sync.WaitGroup
	cancel                          context.CancelFunc
}

func New(c Config, handler std.Handler, policy Errors) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("HTTP handler required")
	}
	v := reflect.ValueOf(handler)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if v.IsNil() {
			return nil, fmt.Errorf("HTTP handler must not be typed nil")
		}
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
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 64
	}
	if c.MaxRequestBytes < 1 || c.MaxResponseBytes < 128 || c.MaxConcurrent < 1 || c.MaxHeaderBytes < 0 {
		return nil, fmt.Errorf("positive HTTP limits required (response >=128)")
	}
	if policy.Status == nil {
		policy.Status = func(w std.ResponseWriter, status int, code string) { std.Error(w, code, status) }
	}
	if policy.Failure == nil {
		policy.Failure = func(w std.ResponseWriter, err error) {
			status := 500
			if errors.Is(err, context.Canceled) {
				status = 408
			} else if errors.Is(err, context.DeadlineExceeded) {
				status = 504
			}
			std.Error(w, std.StatusText(status), status)
		}
	}
	s := &Server{cfg: c, policy: policy, done: make(chan struct{}), slots: make(chan struct{}, c.MaxConcurrent)}
	duration := func(raw string, fallback time.Duration) (time.Duration, error) {
		if raw == "" {
			return fallback, nil
		}
		v, err := time.ParseDuration(raw)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("positive HTTP timeout required")
		}
		return v, nil
	}
	var err error
	s.requestTimeout, err = duration(c.RequestTimeout, 10*time.Second)
	if err != nil {
		return nil, err
	}
	s.shutdownTimeout, err = duration(c.ShutdownTimeout, 5*time.Second)
	if err != nil {
		return nil, err
	}
	server := &std.Server{MaxHeaderBytes: c.MaxHeaderBytes}
	for _, v := range []struct {
		raw      string
		fallback time.Duration
		dst      *time.Duration
	}{{c.ReadHeaderTimeout, 5 * time.Second, &server.ReadHeaderTimeout}, {c.ReadTimeout, 15 * time.Second, &server.ReadTimeout}, {c.WriteTimeout, 15 * time.Second, &server.WriteTimeout}, {c.IdleTimeout, 30 * time.Second, &server.IdleTimeout}} {
		*v.dst, err = duration(v.raw, v.fallback)
		if err != nil {
			return nil, err
		}
	}
	s.handler = s.bound(handler)
	server.Handler = s.handler
	s.server = server
	return s, nil
}
func (s *Server) Handler() std.Handler { return s.handler }

// Addresses consistently returns HTTP URLs, and no addresses before Start.
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
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.listener = l
	base, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.server.BaseContext = func(net.Listener) context.Context { return base }
	go func() {
		err := s.server.Serve(l)
		s.mu.Lock()
		if !errors.Is(err, std.ErrServerClosed) {
			s.serveErr = err
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
	s.used = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	if !started {
		return nil
	}
	stop, cancel := context.WithTimeout(ctx, s.shutdownTimeout)
	defer cancel()
	err := s.server.Shutdown(stop)
	if err != nil {
		err = errors.Join(err, s.server.Close())
	}
	<-s.done
	s.active.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(err, s.serveErr)
}

// Serve starts and supervises this transport, then shuts down with an independent
// bounded context. Parent cancellation is a normal stop.
func (s *Server) Serve(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := s.Start(ctx); err != nil {
		return err
	}
	runErr := s.Run(ctx)
	stopCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	return errors.Join(runErr, s.Stop(stopCtx))
}

type boundedResponse struct {
	header      std.Header
	body        bytes.Buffer
	status, max int
	err         error
}

func (b *boundedResponse) Header() std.Header { return b.header }
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
		b.err = ErrResponseLimit
		return 0, b.err
	}
	return b.body.Write(p)
}
func (s *Server) bound(handler std.Handler) std.Handler {
	return std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			s.policy.Status(w, 503, "closed")
			return
		}
		s.active.Add(1)
		s.mu.Unlock()
		defer s.active.Done()
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			s.policy.Status(w, 429, "capacity")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = std.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes)
		b := &boundedResponse{header: make(std.Header), max: s.cfg.MaxResponseBytes}
		handler.ServeHTTP(b, r)
		if s.policy.NormalizeMuxErrors && (b.status == 404 || b.status == 405) {
			allow := b.header.Get("Allow")
			b.body.Reset()
			b.header = make(std.Header)
			if allow != "" {
				b.header.Set("Allow", allow)
			}
			status := b.status
			b.status = 0
			code := "not_found"
			if status == 405 {
				code = "method_not_allowed"
			}
			s.policy.Status(b, status, code)
		}
		if b.err != nil {
			s.policy.Failure(w, b.err)
			return
		}
		if ctx.Err() != nil && b.status < 400 {
			s.policy.Failure(w, ctx.Err())
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
