package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strconv"
	"testing"
)

// TestMain installs a default trusted-proxy list covering the loopback
// addresses used by the test scenarios. Tests that need a different
// list use withTrustedProxies(t, ...).
func TestMain(m *testing.M) {
	trustedProxies = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	os.Exit(m.Run())
}

func withTrustedProxies(t *testing.T, prefixes []netip.Prefix) {
	t.Helper()
	saved := trustedProxies
	trustedProxies = prefixes
	t.Cleanup(func() { trustedProxies = saved })
}

// scenarios used by both clientIP and homeHandler tests/benchmarks.
// Header-based scenarios use a loopback RemoteAddr so the default
// trusted-proxy list (set in TestMain) honours them.
type scenario struct {
	name       string
	headers    map[string]string
	remoteAddr string
	wantIP     string // expected canonical netip.Addr.String() — empty means invalid
}

var benchScenarios = []scenario{
	{
		name:       "XRealIP_IPv4",
		headers:    map[string]string{"X-Real-IP": "203.0.113.42"},
		remoteAddr: "127.0.0.1:1234",
		wantIP:     "203.0.113.42",
	},
	{
		name:       "XRealIP_IPv6",
		headers:    map[string]string{"X-Real-IP": "2001:db8::1"},
		remoteAddr: "127.0.0.1:1234",
		wantIP:     "2001:db8::1",
	},
	{
		name:       "XForwardedFor_Single",
		headers:    map[string]string{"X-Forwarded-For": "203.0.113.42"},
		remoteAddr: "127.0.0.1:1234",
		wantIP:     "203.0.113.42",
	},
	{
		name:       "XForwardedFor_Chain3",
		headers:    map[string]string{"X-Forwarded-For": "203.0.113.42, 198.51.100.7, 10.0.0.1"},
		remoteAddr: "127.0.0.1:1234",
		wantIP:     "203.0.113.42",
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
		// Untrusted RemoteAddr — proxy headers are ignored even when
		// present, so invalid header content does not matter; clientIP
		// returns the parsed RemoteAddr directly.
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
			if srv := rr.Header().Get("Server"); srv != "" {
				t.Errorf("Server = %q, want empty (no fingerprint leak)", srv)
			}
			if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", cc)
			}
			if cors := rr.Header().Get("Access-Control-Allow-Origin"); cors != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want *", cors)
			}
		})
	}
}

func TestClientIP_TrustModel(t *testing.T) {
	loopbackOnly := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

	cases := []struct {
		name    string
		trusted []netip.Prefix
		s       scenario
		want    string
	}{
		{
			name:    "TrustedRemote_HonoursXRealIP",
			trusted: loopbackOnly,
			s: scenario{
				headers:    map[string]string{"X-Real-IP": "203.0.113.42"},
				remoteAddr: "127.0.0.1:1234",
			},
			want: "203.0.113.42",
		},
		{
			name:    "UntrustedRemote_IgnoresXRealIPSpoof",
			trusted: loopbackOnly,
			s: scenario{
				headers:    map[string]string{"X-Real-IP": "1.1.1.1"},
				remoteAddr: "8.8.8.8:54321",
			},
			want: "8.8.8.8",
		},
		{
			name:    "UntrustedRemote_IgnoresXForwardedForSpoof",
			trusted: loopbackOnly,
			s: scenario{
				headers:    map[string]string{"X-Forwarded-For": "1.1.1.1"},
				remoteAddr: "8.8.8.8:54321",
			},
			want: "8.8.8.8",
		},
		{
			name:    "EmptyTrustList_IgnoresHeaders",
			trusted: nil,
			s: scenario{
				headers:    map[string]string{"X-Real-IP": "1.1.1.1"},
				remoteAddr: "127.0.0.1:1234",
			},
			want: "127.0.0.1",
		},
		{
			name:    "TrustedRemote_BothHeadersInvalid_FallsBackToRemote",
			trusted: loopbackOnly,
			s: scenario{
				headers:    map[string]string{"X-Real-IP": "not-ip", "X-Forwarded-For": "also-not"},
				remoteAddr: "127.0.0.1:1234",
			},
			want: "127.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withTrustedProxies(t, tc.trusted)
			req := newRequest(t.Context(), tc.s)
			got := clientIP(req)
			if !got.IsValid() || got.String() != tc.want {
				t.Errorf("clientIP = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantLen int
		wantErr bool
	}{
		{name: "Empty", in: "", wantLen: 0},
		{name: "SingleIPv4", in: "10.0.0.0/8", wantLen: 1},
		{name: "SingleIPv6", in: "::1/128", wantLen: 1},
		{name: "Multiple", in: "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16", wantLen: 3},
		{name: "Whitespace", in: "  10.0.0.0/8  ,   172.16.0.0/12  ", wantLen: 2},
		{name: "EmptyEntries", in: "10.0.0.0/8,,,", wantLen: 1},
		{name: "Invalid", in: "not-a-cidr", wantErr: true},
		{name: "MissingMask", in: "10.0.0.0", wantErr: true},
		{name: "MixedValidInvalid", in: "10.0.0.0/8,garbage", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTrustedProxies(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseTrustedProxies(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTrustedProxies(%q) error: %v", tc.in, err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("parseTrustedProxies(%q) len = %d, want %d", tc.in, len(got), tc.wantLen)
			}
		})
	}
}

func TestHomeHandler_OPTIONS_Preflight(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	homeHandler(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET, HEAD" {
		t.Errorf("Access-Control-Allow-Methods = %q, want GET, HEAD", got)
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Error("Access-Control-Max-Age missing")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0", rr.Body.Len())
	}
}

func TestHomeHandler_HEAD(t *testing.T) {
	s := scenario{
		name:       "HEAD_XRealIP",
		headers:    map[string]string{"X-Real-IP": "203.0.113.42"},
		remoteAddr: "127.0.0.1:1234",
		wantIP:     "203.0.113.42",
	}
	req := newRequest(t.Context(), s)
	req.Method = http.MethodHead
	rr := httptest.NewRecorder()
	homeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Body.Len(); got != 0 {
		t.Errorf("body length = %d, want 0", got)
	}
	wantCL := strconv.Itoa(len(s.wantIP))
	if got := rr.Header().Get("Content-Length"); got != wantCL {
		t.Errorf("Content-Length = %q, want %q", got, wantCL)
	}
}

func TestHomeHandler_MethodNotAllowed(t *testing.T) {
	for _, m := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodTrace,
	} {
		t.Run(m, func(t *testing.T) {
			req := newRequest(t.Context(), scenario{
				headers:    map[string]string{"X-Real-IP": "203.0.113.42"},
				remoteAddr: "203.0.113.42:54321",
			})
			req.Method = m
			rr := httptest.NewRecorder()
			homeHandler(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rr.Code)
			}
			if got := rr.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
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
