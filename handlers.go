package main

import (
	"net"
	"net/http"

	"log/slog"
)

func homeHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Server", "Atanor 1.0")
	w.Header().Set("Content-Type", "text/plain")

	IPAddress := r.Header.Get("X-Real-Ip")

	if len(IPAddress) == 0 {
		IPAddress = r.Header.Get("X-Forwarded-For")
		if len(IPAddress) == 0 {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)

			if err != nil {
				slog.Error("Request", "Error", err.Error())
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			IPAddress = ip
		}
	}

	ua := r.Header.Get("User-Agent")
	slog.Info("Request", "IP", IPAddress, "User-Agent", ua)

	w.Write([]byte(IPAddress))
}
