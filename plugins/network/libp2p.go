// Package network provides the libp2p transport without exposing libp2p types in core.
package network

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/pluginkit"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	net "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
)

const wireProtocol protocol.ID = "/gochainkit/request/1"

type Config struct {
	IdentityPath    string   `json:"identityPath"`
	Listen          []string `json:"listen"`
	Seeds           []string `json:"seeds"`
	MaxMessageBytes int      `json:"maxMessageBytes"`
	Timeout         string   `json:"timeout"`
}
type networkProtocol interface {
	core.ProtocolBound
	core.GenesisProvider
	core.BlockCodec
}
type Deps struct {
	Protocol networkProtocol `json:"protocol"`
}
type Transport struct {
	mu         sync.RWMutex
	cfg        Config
	descriptor core.ProtocolDescriptor
	genesis    [32]byte
	timeout    time.Duration
	h          host.Host
	handler    func(context.Context, []byte) ([]byte, error)
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	closed     bool
	running    bool
	wg         sync.WaitGroup
	slots      chan struct{}
	outgoing   chan struct{}
}
type envelope struct {
	Protocol core.ProtocolDescriptor
	Genesis  [32]byte
	Payload  []byte
	Error    string
}

func init() { pluginkit.Register("network/libp2p", NewLibp2p) }
func NewLibp2p(cfg Config, d Deps) (*Transport, error) {
	if d.Protocol == nil {
		return nil, fmt.Errorf("network: protocol required")
	}
	if err := d.Protocol.Protocol().Validate(); err != nil {
		return nil, err
	}
	if cfg.MaxMessageBytes == 0 {
		cfg.MaxMessageBytes = 4 << 20
	}
	if cfg.MaxMessageBytes < 128 || cfg.MaxMessageBytes > 32<<20 {
		return nil, fmt.Errorf("network: message limit must be 128..33554432")
	}
	timeout := 5 * time.Second
	if cfg.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(cfg.Timeout)
		if err != nil || timeout <= 0 {
			return nil, fmt.Errorf("network: invalid timeout")
		}
	}
	if len(cfg.Listen) == 0 {
		cfg.Listen = []string{"/ip4/127.0.0.1/tcp/0"}
	}
	if len(cfg.Seeds) > 64 {
		return nil, fmt.Errorf("network: too many seeds")
	}
	for _, addr := range cfg.Listen {
		if _, err := ma.NewMultiaddr(addr); err != nil {
			return nil, err
		}
	}
	for _, addr := range cfg.Seeds {
		if _, err := peer.AddrInfoFromString(addr); err != nil {
			return nil, err
		}
	}
	g, _, err := d.Protocol.Genesis(context.Background())
	if err != nil {
		return nil, err
	}
	encoded, err := d.Protocol.EncodeBlock(g)
	if err != nil {
		return nil, err
	}
	cfg.Listen = append([]string(nil), cfg.Listen...)
	cfg.Seeds = append([]string(nil), cfg.Seeds...)
	return &Transport{cfg: cfg, descriptor: d.Protocol.Protocol(), genesis: sha256.Sum256(encoded), timeout: timeout, slots: make(chan struct{}, 32), outgoing: make(chan struct{}, 32)}, nil
}
func (t *Transport) Protocol() core.ProtocolDescriptor { return t.descriptor }
func (t *Transport) Handle(fn func(context.Context, []byte) ([]byte, error)) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if fn == nil || t.handler != nil || t.closed {
		return fmt.Errorf("network: handler unavailable")
	}
	t.handler = fn
	return nil
}
func identity(path string) (crypto.PrivKey, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return crypto.UnmarshalPrivateKey(data)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil || path == "" {
		return key, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	data, err := crypto.MarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := publishIdentity(path, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			winner, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, readErr
			}
			return crypto.UnmarshalPrivateKey(winner)
		}
		return nil, err
	}
	return key, nil
}

// The final name is published only after a complete synced private temp file.
// Link is exclusive: concurrent creators never replace the winning identity.
func publishIdentity(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()

}
func (t *Transport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.running {
		return fmt.Errorf("network: already started or closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := identity(t.cfg.IdentityPath)
	if err != nil {
		return err
	}
	h, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings(t.cfg.Listen...), libp2p.DisableRelay())
	if err != nil {
		return err
	}
	t.h = h
	t.ctx, t.cancel = context.WithCancel(context.Background())
	t.done = make(chan struct{})
	t.running = true
	h.SetStreamHandler(wireProtocol, t.receive)
	go t.reconnect()
	return nil
}
func (t *Transport) reconnect() {
	defer close(t.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for _, seed := range t.cfg.Seeds {
			ctx, cancel := context.WithTimeout(t.ctx, t.timeout)
			_ = t.Connect(ctx, seed)
			cancel()
		}
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (t *Transport) Stop(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	if !t.running {
		t.mu.Unlock()
		return nil
	}
	t.cancel()
	h := t.h
	done := t.done
	t.mu.Unlock()
	err := h.Close()
	select {
	case <-done:
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
	handlers := make(chan struct{})
	go func() { t.wg.Wait(); close(handlers) }()
	select {
	case <-handlers:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}
func (t *Transport) Addresses() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.h == nil || t.closed {
		return nil
	}
	out := []string{}
	for _, addr := range t.h.Addrs() {
		out = append(out, addr.String()+"/p2p/"+t.h.ID().String())
	}
	return out
}
func peerPart(addr string) string { parts := strings.Split(addr, "/p2p/"); return parts[len(parts)-1] }
func (t *Transport) Peers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.h == nil || t.closed {
		return nil
	}
	out := []string{}
	for _, p := range t.h.Network().Peers() {
		out = append(out, p.String())
	}
	return out
}
func (t *Transport) host() (host.Host, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.h == nil || t.closed {
		return nil, core.ErrClosed
	}
	return t.h, nil
}
func (t *Transport) Connect(ctx context.Context, addr string) error {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	h, err := t.host()
	if err != nil {
		return err
	}
	info, err := peer.AddrInfoFromString(addr)
	if err != nil {
		return err
	}
	return h.Connect(ctx, *info)
}
func (t *Transport) write(w io.Writer, e envelope) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(e.Payload) > t.cfg.MaxMessageBytes || len(data) > 2*t.cfg.MaxMessageBytes+4096 {
		return fmt.Errorf("network: message exceeds limit")
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
func (t *Transport) read(r io.Reader) (envelope, error) {
	var e envelope
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return e, err
	}
	if n > uint32(2*t.cfg.MaxMessageBytes+4096) {
		return e, fmt.Errorf("network: frame exceeds limit")
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return e, err
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return e, err
	}
	if len(e.Payload) > t.cfg.MaxMessageBytes {
		return e, fmt.Errorf("network: payload exceeds limit")
	}
	if e.Protocol != t.descriptor || e.Genesis != t.genesis {
		return e, fmt.Errorf("network: incompatible protocol or genesis")
	}
	return e, nil
}
func (t *Transport) Request(ctx context.Context, id string, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	if len(payload) > t.cfg.MaxMessageBytes {
		return nil, fmt.Errorf("network: payload exceeds limit")
	}
	select {
	case t.outgoing <- struct{}{}:
		defer func() { <-t.outgoing }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	h, err := t.host()
	if err != nil {
		return nil, err
	}
	p, err := peer.Decode(id)
	if err != nil {
		return nil, err
	}
	stream, err := h.NewStream(ctx, p, wireProtocol)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	deadline, _ := ctx.Deadline()
	_ = stream.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	if err := t.write(stream, envelope{Protocol: t.descriptor, Genesis: t.genesis, Payload: payload}); err != nil {
		return nil, err
	}
	response, err := t.read(stream)
	if err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf("remote request rejected")
	}
	return response.Payload, nil
}
func (t *Transport) receive(stream net.Stream) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		stream.Reset()
		return
	}
	select {
	case t.slots <- struct{}{}:
	default:
		t.mu.Unlock()
		stream.Reset()
		return
	}
	t.wg.Add(1)
	handler := t.handler
	base := t.ctx
	t.mu.Unlock()
	defer t.wg.Done()
	defer func() { <-t.slots }()
	defer stream.Close()
	ctx, cancel := context.WithTimeout(base, t.timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	_ = stream.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	request, err := t.read(stream)
	if err != nil {
		stream.Reset()
		return
	}
	response := envelope{Protocol: t.descriptor, Genesis: t.genesis}
	if handler == nil {
		response.Error = "handler unavailable"
	} else {
		response.Payload, err = handler(ctx, request.Payload)
		if err != nil {
			response.Payload = nil
			response.Error = "request rejected"
		}
	}
	if err := t.write(stream, response); err != nil {
		stream.Reset()
	}
}

var _ core.Network = (*Transport)(nil)
