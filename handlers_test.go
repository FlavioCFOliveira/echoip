package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// scenarios used by both clientIP and homeHandler tests/benchmarks.
type scenario struct {
	name       string
	headers    map[string]string
	remoteAddr string
	wantIP     string // expected canonical netip.Addr.String() — empty means invalid
}

var benchScenarios = []scenario{
	{
		name:    "XRealIP_IPv4",
		headers: map[string]string{"X-Real-IP": "203.0.113.42"},
		wantIP:  "203.0.113.42",
	},
	{
		name:    "XRealIP_IPv6",
		headers: map[string]string{"X-Real-IP": "2001:db8::1"},
		wantIP:  "2001:db8::1",
	},
	{
		name:    "XForwardedFor_Single",
		headers: map[string]string{"X-Forwarded-For": "203.0.113.42"},
		wantIP:  "203.0.113.42",
	},
	{
		name:    "XForwardedFor_Chain3",
		headers: map[string]string{"X-Forwarded-For": "203.0.113.42, 198.51.100.7, 10.0.0.1"},
		wantIP:  "203.0.113.42",
	},
	{
		name:       "RemoteAddr_IPv4",
		remoteAddr: "203.0.113.42:54321",
		wantIP:     "203.0.113.42",
	},
	{
		name:       "RemoteAddr_IPv6",
		remoteAddr: "[2001:db8::1]:54321",
		wantIP:     "2001:db8::1",
	},
	{
		name: "Fallthrough_BothHeadersInvalid",
		headers: map[string]string{
			"X-Real-IP":       "not-an-ip",
			"X-Forwarded-For": "also-bogus",
		},
		remoteAddr: "203.0.113.42:54321",
		wantIP:     "203.0.113.42",
	},
}

// errorScenarios produce no valid IP candidate and must yield a zero
// netip.Addr from clientIP and a 500 from homeHandler.
var errorScenarios = []scenario{
	{
		name:       "NoHeaders_InvalidRemoteAddr",
		remoteAddr: "garbage",
	},
	{
		name:       "NoHeaders_EmptyRemoteAddr",
		remoteAddr: "",
	},
}

func newRequest(ctx context.Context, s scenario) *http.Request {
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	// Always overwrite — empty remoteAddr is meaningful in error scenarios.
	// Header-based happy paths return before RemoteAddr is consulted.
	req.RemoteAddr = s.remoteAddr
	return req
}

func TestClientIP(t *testing.T) {
	for _, s := range benchScenarios {
		t.Run(s.name, func(t *testing.T) {
			req := newRequest(t.Context(), s)
			got := clientIP(req)
			if !got.IsValid() {
				t.Fatalf("clientIP returned invalid Addr; want %q", s.wantIP)
			}
			if got.String() != s.wantIP {
				t.Errorf("clientIP = %q, want %q", got.String(), s.wantIP)
			}
		})
	}
}

func TestClientIP_Error(t *testing.T) {
	for _, s := range errorScenarios {
		t.Run(s.name, func(t *testing.T) {
			req := newRequest(t.Context(), s)
			got := clientIP(req)
			if got.IsValid() {
				t.Errorf("clientIP = %q, want invalid Addr", got.String())
			}
		})
	}
}

func TestHomeHandler(t *testing.T) {
	for _, s := range benchScenarios {
		t.Run(s.name, func(t *testing.T) {
			req := newRequest(t.Context(), s)
			rr := httptest.NewRecorder()
			homeHandler(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
			}
			if got := rr.Body.String(); got != s.wantIP {
				t.Errorf("body = %q, want %q", got, s.wantIP)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
				t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", ct)
			}
			if nosniff := rr.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
			}
		})
	}
}

func TestHomeHandler_Error(t *testing.T) {
	for _, s := range errorScenarios {
		t.Run(s.name, func(t *testing.T) {
			req := newRequest(t.Context(), s)
			rr := httptest.NewRecorder()
			homeHandler(rr, req)

			if rr.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500", rr.Code)
			}
		})
	}
}

func BenchmarkClientIP(b *testing.B) {
	for _, s := range benchScenarios {
		b.Run(s.name, func(b *testing.B) {
			req := newRequest(b.Context(), s)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = clientIP(req)
			}
		})
	}
}

func BenchmarkHomeHandler(b *testing.B) {
	for _, s := range benchScenarios {
		b.Run(s.name, func(b *testing.B) {
			req := newRequest(b.Context(), s)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rr := httptest.NewRecorder()
				homeHandler(rr, req)
			}
		})
	}
}

func BenchmarkHomeHandler_Parallel(b *testing.B) {
	for _, s := range []scenario{
		benchScenarios[0], // XRealIP_IPv4 — typical reverse-proxy deployment
		benchScenarios[4], // RemoteAddr_IPv4 — direct exposure
	} {
		b.Run(s.name, func(b *testing.B) {
			req := newRequest(b.Context(), s)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					rr := httptest.NewRecorder()
					homeHandler(rr, req)
				}
			})
		})
	}
}

// BenchmarkHomeHandler_E2E exercises the full Go HTTP stack (TCP
// roundtrip, request parsing, response writing) — closer to real-world
// per-request cost than the in-process variants above.
func BenchmarkHomeHandler_E2E(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(homeHandler))
	defer server.Close()

	client := server.Client()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				b.Fatal(err)
			}
			req.Header.Set("X-Real-IP", "203.0.113.42")
			resp, err := client.Do(req)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}
