package main

import (
	"context"
	"github.com/lengzhao/GoChainKit/assembly"
	_ "github.com/lengzhao/GoChainKit/plugins/all"
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeAndAssemble(t *testing.T) {
	t.Setenv("GOCHAINKIT_KEYSTORE_PASSWORD", "test-password")
	dir := filepath.Join(t.TempDir(), "nested", "authority")
	path, e := initialize(context.Background(), dir, 1700000000)
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	_, _, e = assembly.Load(context.Background(), f)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = initialize(context.Background(), dir, 1700000000); e == nil {
		t.Fatal("overwrote existing setup")
	}
}
