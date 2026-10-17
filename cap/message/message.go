// Package message defines bounded multiplexing above one cap/network.Network handler.
package message

import (
	"context"
)

type Handler func(context.Context, []byte) ([]byte, error)

// Registration exclusively owns a route. Close unregisters it, prevents new
// dispatches and waits for in-flight handlers; it is idempotent. Owners must not
// close a registration synchronously from inside its own handler.
type Registration interface{ Close() error }

// MessageRouter is constructed with one chain-bound Network and is its only
// Handle owner. Start/stop belong to its containing plugin lifecycle. Route IDs
// are versioned and bounded; duplicate/unknown routes fail closed. Requests and
// responses enforce configured byte, concurrency and timeout bounds. Routing
// never bypasses the Network's protocol/genesis authentication, and does not
// provide sender identity when the underlying Network does not expose it.
// Registration errors must leave the route table unchanged. Router shutdown
// cancels and joins active requests before returning.
type MessageRouter interface {
	Register(string, Handler) (Registration, error)
	Request(context.Context, string, string, []byte) ([]byte, error)
}
