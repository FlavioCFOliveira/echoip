package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetrics_ObserveAndRender(t *testing.T) {
	m := &metrics{}
	m.observe(200, 50*time.Microsecond)   // 0.00005s → bucket 0 (<=0.0001)
	m.observe(200, 500*time.Microsecond)  // 0.0005s → bucket 1 (<=0.001)
	m.observe(404, 2*time.Millisecond)    // 0.002s → bucket 2 (<=0.005)
	m.observe(500, 200*time.Millisecond)  // 0.2s → bucket 6 (<=0.5)
	m.observe(200, 5*time.Second)         // > 1s → +Inf

	var buf bytes.Buffer
	m.write(&buf)
	out := buf.String()

	mustContain := []string{
		"echoip_requests_total{class=\"all\"} 5",
		"echoip_requests_total{class=\"4xx\"} 1",
		"echoip_requests_total{class=\"5xx\"} 1",
		"echoip_request_duration_seconds_bucket{le=\"+Inf\"} 5",
		"echoip_request_duration_seconds_count 5",
		"# TYPE echoip_request_duration_seconds histogram",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestMetrics_Middleware_TracksInflight(t *testing.T) {
	m := &metrics{}
	gate := make(chan struct{})
	released := make(chan struct{})
	wrapped := m.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
		close(released)
	}))

	go func() {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
	}()

	// wait for the handler to enter
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.inflight.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if m.inflight.Load() != 1 {
		t.Fatal("inflight did not reach 1")
	}
	close(gate)
	<-released
	// wait for middleware to decrement
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.inflight.Load() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("inflight = %d after release, want 0", m.inflight.Load())
}

func TestMetricsHandler_E2E(t *testing.T) {
	defaultMetrics = &metrics{}
	defaultMetrics.observe(200, 1*time.Millisecond)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	metricsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if !strings.Contains(rr.Body.String(), "echoip_requests_total") {
		t.Errorf("body missing counter\n%s", rr.Body.String())
	}
}
