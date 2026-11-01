// Package http defines optional HTTP transport composition contracts.
package http

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"net/http"
)

// Transport is a bounded HTTP runtime independent of protocol and ledger owners.
type Transport interface {
	core.Lifecycle
	Run(context.Context) error
	Addresses() []string
}

// Server additionally binds a chain API transport to one protocol/ledger owner.
type Server interface {
	Transport
	core.ProtocolBound
	core.LedgerBound
}

// Route patterns use net/http ServeMux method patterns. Providers must not start
// listeners or workers. Handlers must honor request cancellation and return
// promptly, and must not retain the writer or request after returning. Server
// shutdown joins handlers before downstream resources can be closed.
type Route struct {
	Pattern string
	Handler http.Handler
}
type RouteProvider interface {
	core.ProtocolBound
	core.LedgerBound
	Routes() []Route
}
