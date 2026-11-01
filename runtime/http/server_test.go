package http

import (
	"context"
	"errors"
	"io"
	std "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRejectTypedNilHandler(t *testing.T) {
	var mux *std.ServeMux
	for _, handler := range []std.Handler{nil, mux, std.HandlerFunc(nil)} {
		if _, err := New(Config{}, handler, Errors{}); err == nil {
			t.Fatal("nil handler accepted")
		}
	}
}

func TestUnboundHostLimitsAndErrorPolicy(t *testing.T) {
	handler := std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) {
		switch r.URL.Path {
		case "/body":
			_, err := io.ReadAll(r.Body)
			if err != nil {
				std.Error(w, "too large", 413)
			}
		case "/response":
			_, _ = w.Write([]byte(strings.Repeat("x", 129)))
		case "/deadline":
			<-r.Context().Done()
		}
	})
	s, err := New(Config{MaxRequestBytes: 8, MaxResponseBytes: 128, RequestTimeout: "1ms"}, handler, Errors{Status: func(w std.ResponseWriter, status int, code string) { std.Error(w, code, status) }, Failure: func(w std.ResponseWriter, err error) {
		status := 500
		if errors.Is(err, context.DeadlineExceeded) {
			status = 504
		}
		std.Error(w, "failure", status)
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		want       int
	}{{"/body", "123456789", 413}, {"/response", "", 500}, {"/deadline", "", 504}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}

func TestLifecycleJoinsRequestsAndPreservesServeFailure(t *testing.T) {
	ctx := context.Background()
	entered, exited := make(chan struct{}), make(chan struct{})
	s, err := New(Config{Listen: "127.0.0.1:0", MaxConcurrent: 1}, std.HandlerFunc(func(w std.ResponseWriter, r *std.Request) { close(entered); <-r.Context().Done(); close(exited) }), Errors{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := &std.Client{Timeout: time.Second}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		response, err := client.Get(s.Addresses()[0])
		if err == nil {
			response.Body.Close()
		}
	}()
	<-entered
	response, err := client.Get(s.Addresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 429 {
		t.Fatal("concurrency limit ignored")
	}
	if err = s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("handler was not joined")
	}
	<-finished
	if err = s.Start(ctx); err == nil {
		t.Fatal("host reused")
	}
	failed, err := New(Config{Listen: "127.0.0.1:0"}, std.HandlerFunc(func(std.ResponseWriter, *std.Request) {}), Errors{})
	if err != nil {
		t.Fatal(err)
	}
	if err = failed.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_ = failed.listener.Close()
	<-failed.done
	if err = failed.Run(ctx); err == nil {
		t.Fatal("serve failure swallowed")
	}
	if err = failed.Stop(ctx); err == nil {
		t.Fatal("stop failure swallowed")
	}
}
