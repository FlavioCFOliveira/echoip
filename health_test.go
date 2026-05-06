package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzHandler(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	healthzHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

func TestLivezHandler(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/livez", nil)
	rr := httptest.NewRecorder()
	livezHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

func TestReadyzHandler_NotReady(t *testing.T) {
	ready.Store(false)
	t.Cleanup(func() { ready.Store(false) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	readyzHandler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestReadyzHandler_Ready(t *testing.T) {
	ready.Store(true)
	t.Cleanup(func() { ready.Store(false) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	readyzHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

func TestHealthHandlers_HEAD(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"healthz", healthzHandler},
		{"livez", livezHandler},
		{"readyz_ready", readyzHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready.Store(true)
			t.Cleanup(func() { ready.Store(false) })

			req := httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/", nil)
			rr := httptest.NewRecorder()
			tc.handler(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if rr.Body.Len() != 0 {
				t.Errorf("body length = %d, want 0", rr.Body.Len())
			}
			if got := rr.Header().Get("Content-Length"); got != "2" {
				t.Errorf("Content-Length = %q, want %q", got, "2")
			}
		})
	}
}

func TestHealthHandlers_MethodNotAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"healthz", healthzHandler},
		{"livez", livezHandler},
		{"readyz", readyzHandler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
			rr := httptest.NewRecorder()
			tc.handler(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
			if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
			}
		})
	}
}
