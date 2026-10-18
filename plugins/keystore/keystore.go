package keystore

import (
	cap "github.com/lengzhao/GoChainKit/cap/keystore"
	"github.com/lengzhao/GoChainKit/keystore"
	"github.com/lengzhao/pluginkit"
)

type Deps struct {
	Secret cap.SecretSource `json:"secret"`
}

func init() {
	pluginkit.Register("keystore/encrypted-file", NewEncryptedFile)
	pluginkit.Register("keystore/memory", NewMemory)
}
func NewEncryptedFile(c keystore.FileConfig, d Deps) (*keystore.EncryptedFile, error) {
	return keystore.NewEncryptedFile(c, d.Secret)
}
func NewMemory(struct{}, struct{}) (*keystore.Memory, error) { return keystore.NewMemory(), nil }
