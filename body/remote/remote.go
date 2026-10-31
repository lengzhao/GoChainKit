// Package remote serves and fetches bounded canonical transaction bodies.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	bodycap "github.com/lengzhao/GoChainKit/cap/body"
	messagecap "github.com/lengzhao/GoChainKit/cap/message"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	replicationcap "github.com/lengzhao/GoChainKit/cap/replication"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/jsoncodec"
	"sync"
	"time"
)

const Route = "body/transaction/v1"

type Config struct {
	MaxTransactionBytes int    `json:"maxTransactionBytes"`
	MaxPeers            int    `json:"maxPeers"`
	Timeout             string `json:"timeout"`
}
type Deps struct {
	Protocol core.TransactionProtocol `json:"protocol"`
	Network  networkcap.Network       `json:"network"`
	Router   messagecap.MessageRouter `json:"router"`
}
type ServerDeps struct {
	Protocol core.TransactionProtocol  `json:"protocol"`
	Source   bodycap.TransactionSource `json:"source"`
	Router   messagecap.MessageRouter  `json:"router"`
}
type Source struct {
	d       Deps
	c       Config
	timeout time.Duration
}
type Server struct {
	d       ServerDeps
	c       Config
	mu      sync.Mutex
	reg     messagecap.Registration
	closed  bool
	used    bool
	failure error
	fatal   chan struct{}
}
type request struct{ ID []byte }
type response struct {
	Found bool
	Data  []byte
}

func limits(c Config) (Config, time.Duration, error) {
	if c.MaxTransactionBytes == 0 {
		c.MaxTransactionBytes = 2 << 20
	}
	if c.MaxPeers == 0 {
		c.MaxPeers = 16
	}
	timeout := 5 * time.Second
	if c.Timeout != "" {
		var e error
		timeout, e = time.ParseDuration(c.Timeout)
		if e != nil {
			return c, 0, e
		}
	}
	if c.MaxTransactionBytes < 1 || c.MaxTransactionBytes > 32<<20 || c.MaxPeers < 1 || c.MaxPeers > 256 || timeout <= 0 {
		return c, 0, fmt.Errorf("body: invalid remote budget")
	}
	return c, timeout, nil
}
func New(c Config, d Deps) (*Source, error) {
	c, timeout, e := limits(c)
	if e != nil {
		return nil, e
	}
	if d.Protocol == nil || d.Network == nil || d.Router == nil {
		return nil, fmt.Errorf("body: remote dependencies required")
	}
	if e = core.RequireCompatible(d.Protocol.Protocol(), d.Network); e != nil {
		return nil, e
	}
	if bound, ok := d.Router.(core.ProtocolBound); ok {
		if e = core.RequireCompatible(d.Protocol.Protocol(), bound); e != nil {
			return nil, e
		}
	}
	return &Source{d, c, timeout}, nil
}
func (s *Source) Protocol() core.ProtocolDescriptor { return s.d.Protocol.Protocol() }
func (s *Source) Transaction(ctx context.Context, id core.ID) (core.Transaction, error) {
	if id == "" || len(id) > 1024 {
		return nil, fmt.Errorf("body: invalid id")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	raw, _ := json.Marshal(request{[]byte(id)})
	peers := s.d.Network.Peers()
	if len(peers) > s.c.MaxPeers {
		peers = peers[:s.c.MaxPeers]
	}
	var failures error
	for _, peer := range peers {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		data, e := s.d.Router.Request(ctx, peer, Route, raw)
		if e != nil {
			failures = errors.Join(failures, e)
			continue
		}
		if len(data) > 2*s.c.MaxTransactionBytes+128 {
			failures = errors.Join(failures, core.ErrFull)
			continue
		}
		var out response
		if e = jsoncodec.Strict(data, &out); e != nil {
			failures = errors.Join(failures, e)
			continue
		}
		if !out.Found {
			if len(out.Data) != 0 {
				failures = errors.Join(failures, fmt.Errorf("body: malformed missing response"))
			}
			continue
		}
		if len(out.Data) > s.c.MaxTransactionBytes {
			failures = errors.Join(failures, core.ErrFull)
			continue
		}
		tx, e := s.d.Protocol.DecodeTransaction(out.Data)
		if e == nil && (tx == nil || tx.ID() != id) {
			e = fmt.Errorf("body: remote identity mismatch")
		}
		if e == nil {
			e = s.d.Protocol.ValidateTransaction(ctx, tx)
		}
		if e == nil {
			var b []byte
			b, e = s.d.Protocol.EncodeTransaction(tx)
			if e == nil && !bytes.Equal(b, out.Data) {
				e = fmt.Errorf("body: noncanonical remote transaction")
			}
		}
		if e != nil {
			failures = errors.Join(failures, e)
			continue
		}
		return tx, nil
	}
	if failures != nil {
		return nil, failures
	}
	return nil, core.ErrNotFound
}
func NewServer(c Config, d ServerDeps) (*Server, error) {
	c, _, e := limits(c)
	if e != nil {
		return nil, e
	}
	if d.Protocol == nil || d.Source == nil || d.Router == nil {
		return nil, fmt.Errorf("body: server dependencies required")
	}
	if e = core.RequireCompatible(d.Protocol.Protocol(), d.Source); e != nil {
		return nil, e
	}
	if bound, ok := d.Router.(core.ProtocolBound); ok {
		if e = core.RequireCompatible(d.Protocol.Protocol(), bound); e != nil {
			return nil, e
		}
	}
	return &Server{d: d, c: c, fatal: make(chan struct{})}, nil
}
func (s *Server) Protocol() core.ProtocolDescriptor { return s.d.Protocol.Protocol() }
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if s.closed || s.reg != nil {
		return core.ErrClosed
	}
	reg, e := s.d.Router.Register(Route, s.serve)
	if e != nil {
		return e
	}
	s.reg = reg
	return nil
}
func (s *Server) Stop(context.Context) error {
	s.mu.Lock()
	s.closed = true
	reg := s.reg
	s.mu.Unlock()
	if reg != nil {
		return reg.Close()
	}
	return nil
}
func pureMissing(e error) bool {
	if e == core.ErrNotFound {
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return pureMissing(w.Unwrap())
	}
	return false
}
func (s *Server) serve(ctx context.Context, raw []byte) ([]byte, error) {
	if len(raw) > 2048 {
		return nil, core.ErrFull
	}
	var req request
	if e := jsoncodec.Strict(raw, &req); e != nil {
		return nil, e
	}
	if len(req.ID) == 0 || len(req.ID) > 1024 {
		return nil, fmt.Errorf("body: invalid id")
	}
	tx, e := s.d.Source.Transaction(ctx, core.ID(req.ID))
	if pureMissing(e) {
		return json.Marshal(response{})
	}
	if e != nil {
		return nil, s.sourceFailure(e)
	}
	if tx == nil || tx.ID() != core.ID(req.ID) {
		return nil, s.sourceFailure(fmt.Errorf("body: source identity mismatch"))
	}
	if e = s.d.Protocol.ValidateTransaction(ctx, tx); e != nil {
		return nil, s.sourceFailure(e)
	}
	data, e := s.d.Protocol.EncodeTransaction(tx)
	if e != nil {
		return nil, s.sourceFailure(e)
	}
	if len(data) > s.c.MaxTransactionBytes {
		return nil, core.ErrFull
	}
	return json.Marshal(response{true, data})
}

func canceled(e error) bool {
	if e == context.Canceled || e == context.DeadlineExceeded {
		return true
	}
	if many, ok := e.(interface{ Unwrap() []error }); ok {
		if len(many.Unwrap()) == 0 {
			return false
		}
		for _, child := range many.Unwrap() {
			if !canceled(child) {
				return false
			}
		}
		return true
	}
	if w, ok := e.(interface{ Unwrap() error }); ok {
		return canceled(w.Unwrap())
	}
	return false
}
func (s *Server) sourceFailure(e error) error {
	if canceled(e) {
		return e
	}
	local := &replicationcap.LocalServiceError{Cause: e}
	s.mu.Lock()
	if s.failure == nil {
		s.failure = local
		close(s.fatal)
	}
	s.mu.Unlock()
	return local
}

// Run supervises trusted local source failures; configure it as a node worker.
func (s *Server) Run(ctx context.Context) (out error) {
	s.mu.Lock()
	if s.used || s.closed || s.reg == nil {
		s.mu.Unlock()
		return core.ErrClosed
	}
	s.used = true
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		out = ctx.Err()
	case <-s.fatal:
	}
	// Revoke and join handlers before collecting the terminal failure.
	out = errors.Join(out, s.Stop(context.Background()))
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(out, s.failure)
}
