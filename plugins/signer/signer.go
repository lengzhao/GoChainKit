package signer

import (
	suite "github.com/lengzhao/GoChainKit/signer/ed25519"
	custody "github.com/lengzhao/GoChainKit/signer/keystore"
	"github.com/lengzhao/pluginkit"
)

func init() {
	pluginkit.Register("signer/ed25519-keystore", custody.New)
	pluginkit.Register("signer/ed25519-verifier", NewVerifier)
}
func NewVerifier(struct{}, struct{}) (*suite.Verifier, error) { return &suite.Verifier{}, nil }
