package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

// requestIDKey is the context key for the per-request ID. Typed so
// it cannot collide with other packages using context.WithValue.
type requestIDKey struct{}

// accessLogSkip enumerates paths excluded from the access log to
// keep noisy probes (orchestrator health checks, scrapes) out of the
// per-request log stream.
var accessLogSkip = map[string]bool{
	"/healthz": true,
	"/livez":   true,
	"/readyz":  true,
	"/metrics": true,
}

// accessLogMiddleware logs one structured JSON line per non-probe
// request and injects an X-Request-ID into the context and response
// headers. The ID is taken from the upstream X-Request-ID header
// when present (so a tracing proxy can stitch logs end-to-end), or
// generated from 8 random bytes (16 hex chars).
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), requestIDKey{}, rid)
		r = r.WithContext(ctx)

		if accessLogSkip[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)

		addr := clientIP(r)
		client := ""
		if addr.IsValid() {
			client = addr.String()
		}

		slog.LogAttrs(ctx, slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Float64("duration_ms", float64(dur.Microseconds())/1000.0),
			slog.String("client_ip", client),
			slog.String("request_id", rid),
			slog.String("ua", r.UserAgent()),
		)
	})
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
