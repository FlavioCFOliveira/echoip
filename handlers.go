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
	IPAddress := r.Header.Get("X-Real-Ip")
	if len(IPAddress) > 0 {
		w.Write([]byte(IPAddress))
		return
	}

	// checks again if the request is coming from other proxiess
	IPAddress = r.Header.Get("X-Forwarded-For")
	if len(IPAddress) > 0 {
		w.Write([]byte(IPAddress))
		return
	}

	// gets the IP address of the client
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		slog.Error("Request", "Error", err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(ip))
}
