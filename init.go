package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
)

var HOST string
var PORT int

func init() {

	// initializing the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if HOST = os.Getenv("ECHOIP_HOST"); HOST == "" {
		HOST = "0.0.0.0"
	}

	PORT = 8080 // default port
	if portStr := os.Getenv("ECHOIP_PORT"); portStr != "" {
		portint, err := strconv.Atoi(portStr)

		if err != nil {
			slog.Error("Invalid PORT environment variable", "Error", err.Error())
			os.Exit(1)
		}

		PORT = portint
	}

	// initializing the routes
	http.HandleFunc("/", homeHandler)

}
