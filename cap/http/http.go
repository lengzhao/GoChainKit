// Package http defines optional HTTP transport composition contracts.
package http

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
	"net/http"
)

type Server interface {
	core.Lifecycle
	core.ProtocolBound
	core.LedgerBound
	Run(context.Context) error
	Addresses() []string
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
