package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAccessLog_GeneratesIDAndLogs(t *testing.T) {
	buf := captureLog(t)

	wrapped := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	rid := rr.Header().Get("X-Request-ID")
	if len(rid) != 16 {
		t.Errorf("X-Request-ID = %q, want 16 hex chars", rid)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line not JSON: %v\n%s", err, buf.String())
	}
	for _, k := range []string{"method", "path", "status", "duration_ms", "request_id"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("log missing %q\n%s", k, buf.String())
		}
	}
	if entry["request_id"] != rid {
		t.Errorf("log request_id = %v, want %q", entry["request_id"], rid)
	}
}

func TestAccessLog_PropagatesUpstreamID(t *testing.T) {
	buf := captureLog(t)

	wrapped := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "from-upstream")
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Request-ID"); got != "from-upstream" {
		t.Errorf("X-Request-ID echoed = %q, want from-upstream", got)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"request_id":"from-upstream"`)) {
		t.Errorf("log did not propagate upstream ID:\n%s", buf.String())
	}
}

func TestAccessLog_SkipsHealthAndMetrics(t *testing.T) {
	for _, p := range []string{"/healthz", "/livez", "/readyz", "/metrics"} {
		t.Run(p, func(t *testing.T) {
			buf := captureLog(t)
			wrapped := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			wrapped.ServeHTTP(httptest.NewRecorder(), req)
			if buf.Len() != 0 {
				t.Errorf("probe path %s logged: %s", p, buf.String())
			}
		})
	}
}
