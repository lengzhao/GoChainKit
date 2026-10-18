package secret

import (
	"context"
	"fmt"
	"github.com/lengzhao/pluginkit"
	"os"
)

type Config struct {
	Environment string `json:"environment"`
}
type Environment struct{ name string }

func NewEnvironment(c Config, _ struct{}) (*Environment, error) {
	if c.Environment == "" {
		return nil, fmt.Errorf("secret: environment name required")
	}
	return &Environment{c.Environment}, nil
}
func (e *Environment) Secret(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := os.LookupEnv(e.name)
	if !ok || v == "" {
		return nil, fmt.Errorf("secret: environment secret unavailable")
	}
	return []byte(v), nil
}
func init() { pluginkit.Register("secret/env", NewEnvironment) }
