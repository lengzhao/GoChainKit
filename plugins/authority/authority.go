// Package authority registers independent PoA rule, fork-choice and signer adapters.
package authority

import (
	"github.com/lengzhao/GoChainKit/choice/authority"
	"github.com/lengzhao/GoChainKit/rules/poa"
	seal "github.com/lengzhao/GoChainKit/seal/authority"
	"github.com/lengzhao/pluginkit"
)

func newChoice(struct{}, struct{}) (*authority.Choice, error) { return &authority.Choice{}, nil }
func init() {
	pluginkit.Register("rules/poa", poa.New)
	pluginkit.Register("choice/authority", newChoice)
	pluginkit.Register("seal/authority", seal.New)
}
