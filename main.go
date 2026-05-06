package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func main() {
	addr := fmt.Sprintf("%s:%v", HOST, PORT)
	slog.Info("Starting echo-ip service", "host", HOST, "port", PORT)

	server := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Flip readiness just before handing control to the server so
	// /readyz returns 200 once traffic can actually be served.
	ready.Store(true)

	if err := server.ListenAndServe(); err != nil {
		slog.Error("Error starting echo-ip service", "Error", err.Error())
	}
}
