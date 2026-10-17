package model

import (
	"bytes"
	"context"
	"testing"
)

func TestApplyTransitionDeletionAndOwnership(t *testing.T) {
	parent := map[string][]byte{"remove": []byte("x"), "empty": []byte("old")}
	transition := Transition{Writes: map[string][]byte{"empty": {}, "new": []byte("owned")}, Deletes: []string{"remove"}}
	got, err := Apply(context.Background(), parent, transition)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["remove"]; ok {
		t.Fatal("delete became empty value")
	}
	if _, ok := got["empty"]; !ok {
		t.Fatal("empty write became deletion")
	}
	got["new"][0] = 'X'
	if !bytes.Equal(transition.Writes["new"], []byte("owned")) {
		t.Fatal("result aliases transition")
	}
	if string(parent["remove"]) != "x" {
		t.Fatal("parent mutated")
	}
	if _, err := Apply(context.Background(), parent, Transition{Writes: map[string][]byte{"x": nil}, Deletes: []string{"x"}}); err == nil {
		t.Fatal("ambiguous change accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Apply(ctx, parent, transition); err != context.Canceled {
		t.Fatal(err)
	}
}
