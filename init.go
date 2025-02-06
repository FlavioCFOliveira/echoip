package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
)

var host string
var port int

func init() {

	// initializing the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// initializing the flags
	flag.StringVar(&host, "host", "0.0.0.0", "the hostname")
	flag.IntVar(&port, "port", 8010, "the port")
	flag.Parse()

	// initializing the routes
	http.HandleFunc("/", homeHandler)

}
