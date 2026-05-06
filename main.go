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

	server := &http.Server{
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

	if err := run(ctx, server, ln); err != nil {
		slog.Error("Server error", "Error", err.Error())
		os.Exit(1)
	}
	slog.Info("Server stopped cleanly")
}

// run serves until ctx is cancelled or the server fails. On
// cancellation, /readyz is flipped to 503 and server.Shutdown drains
// in-flight requests within shutdownTimeout.
func run(ctx context.Context, server *http.Server, ln net.Listener) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
