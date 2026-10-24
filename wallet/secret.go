package wallet

import (
	"context"
	"fmt"
	"os"
)

// EnvironmentSecret reads the operator's password at use time; no password is
// written into node configuration, image layers, or wallet CLI arguments.
type EnvironmentSecret struct{}

func (EnvironmentSecret) Secret(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := os.Getenv("GOCHAINKIT_KEYSTORE_PASSWORD")
	if value == "" {
		return nil, fmt.Errorf("GOCHAINKIT_KEYSTORE_PASSWORD is required")
	}
	return []byte(value), nil
}
