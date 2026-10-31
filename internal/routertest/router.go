// Package routertest starts real routers for network integration fixtures.
package routertest

import (
	"context"
	networkcap "github.com/lengzhao/GoChainKit/cap/network"
	"github.com/lengzhao/GoChainKit/message/router"
	"testing"
)

func Start(t testing.TB, n networkcap.Network) *router.Router {
	t.Helper()
	r, e := router.New(router.Config{}, router.Deps{Network: n})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := r.Stop(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return r
}
