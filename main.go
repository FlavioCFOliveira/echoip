package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	addr := fmt.Sprintf("%s:%v", HOST, PORT)
	slog.Info("Starting echo-ip service", "host", HOST, "port", PORT)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		slog.Error("Failed to bind listener", "Error", err.Error())
		os.Exit(1)
	}
	if ProxyProtocol {
		ln = &proxyProtoListener{Listener: ln}
		slog.Info("PROXY protocol decoder enabled on listener")
	}
	if MaxConns > 0 {
		ln = newConnLimitListener(ln, MaxConns)
		slog.Info("Connection limit enabled on listener", "max", MaxConns)
	}

	var rl *rateLimiter
	if RateLimit > 0 {
		rl = newRateLimiter(RateLimit)
		go rl.startCleanup(ctx, time.Minute, 10*time.Minute)
		slog.Info("Per-IP rate limit enabled", "requests_per_minute", RateLimit)
	}

	server := &http.Server{
		Handler:           routes(rl),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Route the package "log" output net/http uses for accept,
		// TLS handshake, and panic-handler errors through the same
		// JSON slog handler everything else uses.
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	// Listener is bound — flip readiness before serving so /readyz
	// picks up immediately rather than racing the goroutine.
	ready.Store(true)

	if err := run(ctx, server, ln, TLSCert, TLSKey); err != nil {
		slog.Error("Server error", "Error", err.Error())
		os.Exit(1)
	}
	slog.Info("Server stopped cleanly")
}

// routes builds the dedicated *http.ServeMux. Tests construct their
// own mux for isolation; main wires this one into the production
// http.Server. http.DefaultServeMux is intentionally unused so that
// future imports cannot register conflicting routes via init().
//
// rl is applied only to / so health probes are never throttled.
func routes(rl *rateLimiter) *http.ServeMux {
	mux := http.NewServeMux()
	var home http.Handler = http.HandlerFunc(homeHandler)
	if rl != nil {
		home = rl.middleware(home)
	}
	mux.Handle("/", home)
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/livez", livezHandler)
	mux.HandleFunc("/readyz", readyzHandler)
	return mux
}

// run serves until ctx is cancelled or the server fails. On
// cancellation, /readyz is flipped to 503 and server.Shutdown drains
// in-flight requests within shutdownTimeout. If both certFile and
// keyFile are non-empty, the listener is upgraded to TLS via
// server.ServeTLS; otherwise plain HTTP is served.
func run(ctx context.Context, server *http.Server, ln net.Listener, certFile, keyFile string) error {
	serverErr := make(chan error, 1)
	go func() {
		var err error
		if certFile != "" && keyFile != "" {
			err = server.ServeTLS(ln, certFile, keyFile)
		} else {
			err = server.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		slog.Info("Shutdown signal received, draining in-flight requests")
		ready.Store(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err, ok := <-serverErr:
		if ok && err != nil {
			return err
		}
		return nil
	}
}
