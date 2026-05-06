package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionHandler(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/version", nil)
	rr := httptest.NewRecorder()
	versionHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"version:", "commit:", "date:", "go:"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nfull body:\n%s", want, body)
		}
	}
	if got := rr.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestVersionHandler_HEAD(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/version", nil)
	rr := httptest.NewRecorder()
	versionHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0", rr.Body.Len())
	}
}

func TestVersionHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/version", nil)
	rr := httptest.NewRecorder()
	versionHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}
