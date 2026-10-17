package jsoncodec

import "testing"

func TestDecodePolicies(t *testing.T) {
	type record struct{ Value []byte }
	for _, data := range []string{`{"Value":null,"extra":0}`, `{"Value":null} {}`, `{"Value":null,"Value":null}`} {
		var out record
		if err := Canonical([]byte(data), &out); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	var out record
	if err := Strict([]byte(" {\"Value\":null} "), &out); err != nil {
		t.Fatal(err)
	}
	if err := Canonical([]byte(" {\"Value\":null} "), &out); err == nil {
		t.Fatal("canonical accepted whitespace")
	}
	if err := Canonical([]byte(`{"Value":""}`), &out); err != nil || out.Value == nil {
		t.Fatalf("empty distinction lost: %v", err)
	}
	if err := Canonical([]byte(`{"Value":null}`), &out); err != nil || out.Value != nil {
		t.Fatalf("nil distinction lost: %v", err)
	}
}
