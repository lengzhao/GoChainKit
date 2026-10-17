package core

import (
	"context"
	"testing"
)

func TestApplyStateChangesRejectsAmbiguityAndKeepsParent(t *testing.T) {
	parent := map[string][]byte{"gone": []byte("x"), "keep": []byte("y")}
	got, err := ApplyStateChanges(context.Background(), parent, map[string][]byte{"empty": nil}, []string{"gone"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got["gone"]; exists {
		t.Fatal("deleted key still exists")
	}
	if _, exists := got["empty"]; !exists {
		t.Fatal("nil write treated as deletion")
	}
	got["keep"][0] = 'z'
	if string(parent["keep"]) != "y" {
		t.Fatal("parent aliased")
	}
	for _, deletes := range [][]string{{"a", "a"}, {"empty"}} {
		if _, err := ApplyStateChanges(context.Background(), parent, map[string][]byte{"empty": nil}, deletes); err == nil {
			t.Fatal("ambiguous changes accepted")
		}
	}
}
