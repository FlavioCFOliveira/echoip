package main

import (
	"fmt"
	"log/slog"
	"net/http"
)

func main() {
	slog.Info("Starting echo-ip service", "host", HOST, "port", PORT)

	err := http.ListenAndServe(fmt.Sprintf("%s:%v", HOST, PORT), nil)
	if err != nil {
		slog.Error("Error starting echo-ip service", "Error", err.Error())
	}

}
