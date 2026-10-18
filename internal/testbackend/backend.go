// Package testbackend provides checked backend factories for contract tests.
package testbackend

import (
	capstorage "github.com/lengzhao/GoChainKit/cap/storage"
	"github.com/lengzhao/GoChainKit/storage/backend"
	"testing"
)

func Bolt(t testing.TB, path string) capstorage.Backend {
	t.Helper()
	v, e := backend.NewBolt(backend.BoltConfig{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
