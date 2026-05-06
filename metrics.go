package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// metrics holds the request-side counters and a fixed-bucket
// histogram for request duration in seconds. Buckets are chosen for
// an IP-echo workload where p99 should sit well under 10 ms.
type metrics struct {
	requestsTotal  atomic.Uint64
	requests4xx    atomic.Uint64
	requests5xx    atomic.Uint64
	inflight       atomic.Int64
	histogram      [9]atomic.Uint64 // bucket counts per upper bound + the +Inf bucket
	histogramSum   atomic.Uint64    // sum of durations, microseconds
	histogramCount atomic.Uint64
}

var (
	defaultMetrics = &metrics{}

	// Histogram upper bounds, seconds. The final +Inf bucket lives in
	// histogram[len(bucketBounds)].
	bucketBounds = [...]float64{
		0.0001,
		0.001,
		0.005,
		0.01,
		0.05,
		0.1,
		0.5,
		1.0,
	}
)

// observe records one completed request. It increments the smallest
// histogram bucket whose upper bound is >= duration; cumulative
// rendering happens at /metrics time.
func (m *metrics) observe(status int, duration time.Duration) {
	m.requestsTotal.Add(1)
	if status >= 500 {
		m.requests5xx.Add(1)
	} else if status >= 400 {
		m.requests4xx.Add(1)
	}

	seconds := duration.Seconds()
	idx := len(bucketBounds) // +Inf bucket
	for i, b := range bucketBounds {
		if seconds <= b {
			idx = i
			break
		}
	}
	m.histogram[idx].Add(1)
	// #nosec G115 -- duration is always >= 0 (from time.Since on a
	// monotonic start time captured at the entry of the same call).
	m.histogramSum.Add(uint64(duration.Microseconds()))
	m.histogramCount.Add(1)
}

// statusWriter captures the response status code for the metrics
// middleware. Lossy on the Hijacker / Flusher interfaces, which the
// echoip handlers do not use.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// metricsMiddleware times every request and records it on m. It does
// not capture the body bytes — that is what request-ID / access-log
// middleware is for (see roadmap task #20).
func (m *metrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inflight.Add(1)
		defer m.inflight.Add(-1)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		m.observe(sw.status, time.Since(start))
	})
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if !methodAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	defaultMetrics.write(w)
}

func (m *metrics) write(w io.Writer) {
	wf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format, args...)
	}

	total := m.requestsTotal.Load()
	c4 := m.requests4xx.Load()
	c5 := m.requests5xx.Load()
	in := m.inflight.Load()

	wf("# HELP echoip_requests_total Total HTTP requests served, partitioned by status class.\n")
	wf("# TYPE echoip_requests_total counter\n")
	wf("echoip_requests_total{class=\"all\"} %d\n", total)
	wf("echoip_requests_total{class=\"4xx\"} %d\n", c4)
	wf("echoip_requests_total{class=\"5xx\"} %d\n", c5)

	wf("# HELP echoip_inflight_requests Requests currently being served.\n")
	wf("# TYPE echoip_inflight_requests gauge\n")
	wf("echoip_inflight_requests %d\n", in)

	wf("# HELP echoip_request_duration_seconds Histogram of request handler durations.\n")
	wf("# TYPE echoip_request_duration_seconds histogram\n")
	cumulative := uint64(0)
	for i, b := range bucketBounds {
		cumulative += m.histogram[i].Load()
		wf("echoip_request_duration_seconds_bucket{le=\"%s\"} %d\n",
			strconv.FormatFloat(b, 'g', -1, 64), cumulative)
	}
	cumulative += m.histogram[len(bucketBounds)].Load()
	wf("echoip_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", cumulative)
	wf("echoip_request_duration_seconds_sum %s\n",
		strconv.FormatFloat(float64(m.histogramSum.Load())/1e6, 'g', -1, 64))
	wf("echoip_request_duration_seconds_count %d\n", m.histogramCount.Load())
}
