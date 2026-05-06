package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRoutes_AllPathsRegistered(t *testing.T) {
	mux := routes()
	for _, p := range []string{"/", "/healthz", "/livez", "/readyz"} {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			// /readyz returns 503 unless ready is set; we only assert
			// the route is wired (not 404).
			if rr.Code == http.StatusNotFound {
				t.Errorf("path %q routed to NotFound", p)
			}
		})
	}
}

// TestRun_GracefulShutdownDrainsInflight verifies that an in-flight
// request started before context cancellation completes successfully,
// and that run() returns within the shutdown deadline.
func TestRun_GracefulShutdownDrainsInflight(t *testing.T) {
	started := make(chan struct{})
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "drained")
		}),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, server, ln) }()

	type result struct {
		status int
		body   string
	}
	reqDone := make(chan result, 1)
	reqErr := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			reqErr <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		reqDone <- result{status: resp.StatusCode, body: string(body)}
	}()

	<-started
	cancel() // simulate SIGTERM arrival mid-request

	select {
	case r := <-reqDone:
		if r.status != http.StatusOK {
			t.Errorf("status = %d, want 200", r.status)
		}
		if r.body != "drained" {
			t.Errorf("body = %q, want %q", r.body, "drained")
		}
	case err := <-reqErr:
		t.Fatalf("inflight request failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("inflight request timed out")
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after shutdown")
	}
}

// TestRun_ServerErrorPropagates verifies that a server-side error
// (e.g. listener already closed) is returned from run, and ctx
// cancellation is not required to unblock.
func TestRun_ServerErrorPropagates(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // force Serve to fail immediately

	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	}

	done := make(chan error, 1)
	go func() { done <- run(t.Context(), server, ln) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("run returned nil, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return on server error")
	}
}
