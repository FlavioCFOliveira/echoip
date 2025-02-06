package main

import (
	"fmt"
	"log/slog"
	"net/http"
)

func main() {
	slog.Info("Starting echo-ip service", "host", host, "port", port)

	err := http.ListenAndServe(fmt.Sprintf("%s:%v", host, port), nil)
	if err != nil {
		slog.Error("Error starting echo-ip service", "Error", err.Error())
	}
}
