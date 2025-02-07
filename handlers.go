package main

import (
	"net"
	"net/http"

	"log/slog"
)

func homeHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Server", "Echo Server 1.0")
	w.Header().Set("Content-Type", "text/plain")

	// checks if the request is coming from a proxy
	ipAddress := r.Header.Get("X-Real-IP")
	if ipAddress != "" {
		w.Write([]byte(ipAddress))
		return
	}

	// checks again if the request is coming from other proxies
	ipAddress = r.Header.Get("X-Forwarded-For")
	if ipAddress != "" {
		w.Write([]byte(ipAddress))
		return
	}

	// gets the IP address of the client
	ipAddress, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		slog.Error("Request", "Error", err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(ipAddress))
}
