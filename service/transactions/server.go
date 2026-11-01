// Package transactions composes native BTC/ETH transaction tools into a standalone
// HTTP service. It has no ledger, wallet custody, broadcast or consensus dependency.
package transactions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	host "github.com/lengzhao/GoChainKit/runtime/http"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Bitcoin tools retain native types. Implementations must be concurrency-safe,
// honor cancellation between bounded operations and not retain input references.
type Bitcoin interface {
	Decode([]byte) (*wire.MsgTx, error)
	Encode(*wire.MsgTx) ([]byte, error)
	VerifyScripts(context.Context, *wire.MsgTx, map[wire.OutPoint]*wire.TxOut) error
}

// Ethereum tools recover a sender under their configured chain signing domain.
// Implementations have the same ownership and concurrency contract as Bitcoin.
type Ethereum interface {
	Decode([]byte) (*types.Transaction, error)
	Encode(*types.Transaction) ([]byte, error)
	Sender(context.Context, *types.Transaction) (common.Address, error)
}
type Deps struct {
	Bitcoin  Bitcoin  `json:"bitcoin"`
	Ethereum Ethereum `json:"ethereum"`
}
type Config struct {
	Listen          string `json:"listen"`
	MaxRequestBytes int64  `json:"maxRequestBytes"`
	MaxConcurrent   int    `json:"maxConcurrent"`
}

// Server owns its listener. New performs no I/O. Run is single-use and joins the
// server on cancellation; Handler can also be mounted in a caller-owned server.
type Server struct {
	deps    Deps
	handler http.Handler
	host    *host.Server
	used    atomic.Bool
	running atomic.Bool
}

func New(c Config, d Deps) (*Server, error) {
	for _, dep := range []any{d.Bitcoin, d.Ethereum} {
		if dep == nil {
			continue
		}
		v := reflect.ValueOf(dep)
		switch v.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
			if v.IsNil() {
				return nil, fmt.Errorf("transactions: typed nil dependency")
			}
		}
	}
	if d.Bitcoin == nil && d.Ethereum == nil {
		return nil, fmt.Errorf("transactions: at least one tool dependency required")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8090"
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = 12 << 20
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 8
	}
	if c.MaxRequestBytes < 1 || c.MaxRequestBytes > 64<<20 || c.MaxConcurrent < 1 || c.MaxConcurrent > 1024 {
		return nil, fmt.Errorf("transactions: invalid request or concurrency limit")
	}
	s := &Server{deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, struct {
			Status string `json:"status"`
		}{"ok"})
	})
	if d.Bitcoin != nil {
		mux.HandleFunc("POST /v1/bitcoin/decode", s.bitcoinDecode)
		mux.HandleFunc("POST /v1/bitcoin/verify-scripts", s.bitcoinVerify)
	}
	if d.Ethereum != nil {
		mux.HandleFunc("POST /v1/ethereum/decode", s.ethereumDecode)
		mux.HandleFunc("POST /v1/ethereum/sender", s.ethereumSender)
	}
	h, err := host.New(host.Config{Listen: c.Listen, MaxRequestBytes: c.MaxRequestBytes, MaxConcurrent: c.MaxConcurrent, MaxHeaderBytes: 16 << 10}, mux, host.Errors{
		Status: fail,
		Failure: func(w http.ResponseWriter, err error) {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				operationError(w, err)
			} else {
				fail(w, 500, "internal")
			}
		},
	})
	if err != nil {
		return nil, err
	}
	s.host = h
	s.handler = h.Handler()
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.handler }

// Addresses preserves the tool service's legacy host:port representation.
func (s *Server) Addresses() []string {
	if !s.running.Load() {
		return nil
	}
	urls := s.host.Addresses()
	for i := range urls {
		urls[i] = strings.TrimPrefix(urls[i], "http://")
	}
	return urls
}
func (s *Server) Run(ctx context.Context) error {
	if !s.used.CompareAndSwap(false, true) {
		return fmt.Errorf("transactions: service already run")
	}
	s.running.Store(true)
	defer s.running.Store(false)
	return s.host.Serve(ctx)
}

type dataRequest struct {
	Data []byte `json:"data"`
}
type prevout struct {
	TransactionID string `json:"transactionID"`
	Index         uint32 `json:"index"`
	Value         int64  `json:"value,string"`
	Script        []byte `json:"script"`
}
type bitcoinRequest struct {
	Data     []byte    `json:"data"`
	Prevouts []prevout `json:"prevouts"`
}
type result struct {
	ID     string `json:"id"`
	Data   []byte `json:"data"`
	Sender string `json:"sender,omitempty"`
}

func (s *Server) read(w http.ResponseWriter, r *http.Request, out any) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, 413, "too_large")
		} else {
			fail(w, 400, "invalid_input")
		}
		return false
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		fail(w, 400, "invalid_input")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid_input")
		return false
	}
	if err := r.Context().Err(); err != nil {
		operationError(w, err)
		return false
	}
	return true
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	write(w, status, struct {
		Error string `json:"error"`
	}{code})
}
func operationError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		fail(w, 408, "canceled")
	} else if errors.Is(err, context.DeadlineExceeded) {
		fail(w, 504, "timeout")
	} else {
		fail(w, 422, "verification_failed")
	}
}
func (s *Server) bitcoinDecode(w http.ResponseWriter, r *http.Request) {
	var req dataRequest
	if !s.read(w, r, &req) {
		return
	}
	tx, err := s.deps.Bitcoin.Decode(req.Data)
	if err != nil {
		fail(w, 400, "invalid_transaction")
		return
	}
	raw, err := s.deps.Bitcoin.Encode(tx)
	if err != nil {
		fail(w, 500, "internal")
		return
	}
	write(w, 200, result{ID: tx.TxHash().String(), Data: raw})
}
func (s *Server) bitcoinVerify(w http.ResponseWriter, r *http.Request) {
	var req bitcoinRequest
	if !s.read(w, r, &req) {
		return
	}
	tx, err := s.deps.Bitcoin.Decode(req.Data)
	if err != nil {
		fail(w, 400, "invalid_transaction")
		return
	}
	prev := make(map[wire.OutPoint]*wire.TxOut, len(req.Prevouts))
	for _, out := range req.Prevouts {
		hash, err := chainhash.NewHashFromStr(out.TransactionID)
		if err != nil || len(out.TransactionID) != 64 || out.Value < 0 {
			fail(w, 400, "invalid_prevout")
			return
		}
		point := wire.OutPoint{Hash: *hash, Index: out.Index}
		if _, exists := prev[point]; exists {
			fail(w, 400, "duplicate_prevout")
			return
		}
		prev[point] = wire.NewTxOut(out.Value, out.Script)
	}
	if err = s.deps.Bitcoin.VerifyScripts(r.Context(), tx, prev); err != nil {
		operationError(w, err)
		return
	}
	raw, err := s.deps.Bitcoin.Encode(tx)
	if err != nil {
		fail(w, 500, "internal")
		return
	}
	write(w, 200, result{ID: tx.TxHash().String(), Data: raw})
}
func (s *Server) ethereumDecode(w http.ResponseWriter, r *http.Request) { s.ethereum(w, r, false) }
func (s *Server) ethereumSender(w http.ResponseWriter, r *http.Request) { s.ethereum(w, r, true) }
func (s *Server) ethereum(w http.ResponseWriter, r *http.Request, recoverSender bool) {
	var req dataRequest
	if !s.read(w, r, &req) {
		return
	}
	tx, err := s.deps.Ethereum.Decode(req.Data)
	if err != nil {
		fail(w, 400, "invalid_transaction")
		return
	}
	out := result{ID: tx.Hash().Hex()}
	if recoverSender {
		sender, err := s.deps.Ethereum.Sender(r.Context(), tx)
		if err != nil {
			operationError(w, err)
			return
		}
		out.Sender = sender.Hex()
	}
	out.Data, err = s.deps.Ethereum.Encode(tx)
	if err != nil {
		fail(w, 500, "internal")
		return
	}
	write(w, 200, out)
}
