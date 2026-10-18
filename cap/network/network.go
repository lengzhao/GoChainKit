package network

import (
	"context"
	"github.com/lengzhao/GoChainKit/core"
)

// Network transports opaque, bounded protocol messages between authenticated
// peer identities. Chain membership is checked separately from peer identity.
type Network interface {
	core.Lifecycle
	core.ProtocolBound
	Addresses() []string
	Peers() []string
	Connect(context.Context, string) error
	Request(context.Context, string, []byte) ([]byte, error)
	Handle(func(context.Context, []byte) ([]byte, error)) error
}

// Libp2pIdentity supplies caller-owned serialized libp2p private key bytes.
// This local transport capability does not require generic signers to export keys.
type Libp2pIdentity interface {
	PrivateKey(context.Context) ([]byte, error)
}
