// Package router multiplexes bounded versioned routes over one chain-bound Network.
package router

import (
	"context"
	"encoding/binary"
	"fmt"
	messagecap "github.com/lengzhao/GoChainKit/cap/message"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/core"
	"regexp"
	"sync"
	"time"
)

type Config struct {
	MaxBytes      int    `json:"maxBytes"`
	MaxConcurrent int    `json:"maxConcurrent"`
	Timeout       string `json:"timeout"`
}
type Deps struct {
	Network networkcap.Network `json:"network"`
}
type Router struct {
	network         networkcap.Network
	max             int
	timeout         time.Duration
	slots           chan struct{}
	outgoing        chan struct{}
	mu              sync.Mutex
	routes          map[string]*registration
	started, closed bool
	life            context.Context
	cancel          context.CancelFunc
	active          sync.WaitGroup
	done            chan struct{}
}
type registration struct {
	owner   *Router
	name    string
	handler messagecap.Handler
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	active  sync.WaitGroup
}

var routePattern = regexp.MustCompile(`^[a-z][a-z0-9._/-]{0,119}/v[1-9][0-9]{0,4}$`)

func New(c Config, d Deps) (*Router, error) {
	if d.Network == nil {
		return nil, fmt.Errorf("router: network required")
	}
	if e := d.Network.Protocol().Validate(); e != nil {
		return nil, e
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 6 << 20
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 32
	}
	timeout := 5 * time.Second
	if c.Timeout != "" {
		var e error
		timeout, e = time.ParseDuration(c.Timeout)
		if e != nil {
			return nil, e
		}
	}
	if c.MaxBytes < 1 || c.MaxBytes > 64<<20 || c.MaxConcurrent < 1 || c.MaxConcurrent > 4096 || timeout <= 0 {
		return nil, fmt.Errorf("router: invalid limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Router{network: d.Network, max: c.MaxBytes, timeout: timeout, slots: make(chan struct{}, c.MaxConcurrent), outgoing: make(chan struct{}, c.MaxConcurrent), routes: map[string]*registration{}, life: ctx, cancel: cancel, done: make(chan struct{})}, nil
}
func (r *Router) Protocol() core.ProtocolDescriptor { return r.network.Protocol() }
func (r *Router) Register(name string, h messagecap.Handler) (messagecap.Registration, error) {
	if !routePattern.MatchString(name) || h == nil {
		return nil, fmt.Errorf("router: invalid route")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, core.ErrClosed
	}
	if _, ok := r.routes[name]; ok {
		return nil, fmt.Errorf("router: duplicate route")
	}
	ctx, cancel := context.WithCancel(r.life)
	reg := &registration{owner: r, name: name, handler: h, ctx: ctx, cancel: cancel}
	r.routes[name] = reg
	return reg, nil
}
func (r *Router) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.started {
		return core.ErrClosed
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	// Handle only installs a callback; no network IO or dispatch is performed here.
	if e := r.network.Handle(r.dispatch); e != nil {
		return e
	}
	r.started = true
	return nil
}
func (r *Router) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
		for _, reg := range r.routes {
			reg.closed = true
			reg.cancel()
		}
		r.routes = map[string]*registration{}
		go func() { r.active.Wait(); close(r.done) }()
	}
	done := r.done
	r.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (reg *registration) Close() error {
	r := reg.owner
	r.mu.Lock()
	if !reg.closed {
		reg.closed = true
		reg.cancel()
		if r.routes[reg.name] == reg {
			delete(r.routes, reg.name)
		}
	}
	r.mu.Unlock()
	reg.active.Wait()
	return nil
}
func (r *Router) begin(ctx context.Context) (context.Context, func(), error) {
	r.mu.Lock()
	if r.closed || !r.started {
		r.mu.Unlock()
		return nil, nil, core.ErrClosed
	}
	r.active.Add(1)
	r.mu.Unlock()
	request, cancel := context.WithTimeout(ctx, r.timeout)
	stop := context.AfterFunc(r.life, cancel)
	return request, func() { stop(); cancel(); r.active.Done() }, nil
}
func (r *Router) Request(ctx context.Context, peer, route string, data []byte) ([]byte, error) {
	if !routePattern.MatchString(route) || len(data) > r.max {
		return nil, fmt.Errorf("router: request limit or route")
	}
	ctx, finish, e := r.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer finish()
	select {
	case r.outgoing <- struct{}{}:
		defer func() { <-r.outgoing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(route)))
	frame = append(frame, route...)
	frame = append(frame, data...)
	raw, e := r.network.Request(ctx, peer, frame)
	if e != nil {
		return nil, e
	}
	if len(raw) > r.max {
		return nil, fmt.Errorf("router: response limit")
	}
	return append([]byte(nil), raw...), nil
}
func (r *Router) dispatch(ctx context.Context, raw []byte) ([]byte, error) {
	if len(raw) < 2 || len(raw) > r.max+130 {
		return nil, fmt.Errorf("router: frame limit")
	}
	n := int(binary.BigEndian.Uint16(raw))
	if n == 0 || n > 128 || len(raw) < 2+n || len(raw)-2-n > r.max {
		return nil, fmt.Errorf("router: malformed frame")
	}
	route := string(raw[2 : 2+n])
	r.mu.Lock()
	reg := r.routes[route]
	if r.closed || !r.started || reg == nil || reg.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("router: route unavailable")
	}
	r.active.Add(1)
	reg.active.Add(1)
	r.mu.Unlock()
	defer r.active.Done()
	defer reg.active.Done()
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	stop := context.AfterFunc(reg.ctx, cancel)
	defer stop()
	defer cancel()
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	out, e := reg.handler(ctx, append([]byte(nil), raw[2+n:]...))
	if e != nil {
		return nil, e
	}
	if len(out) > r.max {
		return nil, fmt.Errorf("router: response limit")
	}
	return append([]byte(nil), out...), nil
}

var _ messagecap.MessageRouter = (*Router)(nil)
