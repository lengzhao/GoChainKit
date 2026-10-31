package height

import (
	"context"
	"encoding/json"
	"github.com/lengzhao/GoChainKit/core"
	"github.com/lengzhao/GoChainKit/internal/testprotocol"
	"net/http"
	"net/http/httptest"
	"testing"
)

type index struct {
	*testprotocol.Format
	owner *core.LedgerIdentity
}

func (i index) LedgerID() *core.LedgerIdentity { return i.owner }
func (i index) AtHeight(_ context.Context, id core.ID, h uint64) (core.Block, error) {
	if id != "anchor" || h != 0 {
		return nil, core.ErrNotFound
	}
	return testprotocol.Frame{}, nil
}
func TestHeightHTTPRequiresAnchor(t *testing.T) {
	p := testprotocol.New("height")
	r, e := New(Config{}, Deps{Protocol: p, Index: index{p, core.NewLedgerIdentity()}})
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	for _, route := range r.Routes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	for _, tc := range []struct {
		url  string
		code int
	}{{"/v1/chain/heights/0?at=616e63686f72", 200}, {"/v1/chain/heights/0", 400}, {"/v1/chain/heights/x?at=616e63686f72", 400}, {"/v1/chain/heights/1?at=616e63686f72", 404}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.url, nil))
		if w.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.url, w.Code, w.Body)
		}
		if tc.code == 200 {
			var v map[string]any
			if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil || v["height"] != "0" {
				t.Fatal(v, e)
			}
		}
	}
}
