package secret

import (
	"context"
	"testing"
)

func TestEnvironmentReadAtUse(t *testing.T) {
	const name = "GOCHAINKIT_SECRET_UNIT_TEST"
	t.Setenv(name, "")
	s, e := NewEnvironment(Config{Environment: name}, struct{}{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Secret(context.Background()); e == nil {
		t.Fatal("empty secret accepted")
	}
	t.Setenv(name, "first")
	v, e := s.Secret(context.Background())
	if e != nil || string(v) != "first" {
		t.Fatal(e)
	}
	t.Setenv(name, "second")
	next, _ := s.Secret(context.Background())
	if string(next) != "second" {
		t.Fatal("secret cached")
	}
	v[0] = 0
	if string(next) != "second" {
		t.Fatal("secret aliased")
	}
}
