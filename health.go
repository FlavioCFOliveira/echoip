package main

import (
	"io"
	"net/http"
	"sync/atomic"
)

// ready flips to true once main has handed the listener to the HTTP
// server. /readyz uses it to gate traffic during cold start.
var ready atomic.Bool

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	if !methodAllowed(w, r) {
		return
	}
	writeOK(w, r)
}

func livezHandler(w http.ResponseWriter, r *http.Request) {
	if !methodAllowed(w, r) {
		return
	}
	writeOK(w, r)
}

func readyzHandler(w http.ResponseWriter, r *http.Request) {
	if !methodAllowed(w, r) {
		return
	}
	if !ready.Load() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	writeOK(w, r)
}

func writeOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", "2")
		return
	}
	_, _ = io.WriteString(w, "ok")
}

func methodAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return false
	}
	return true
}
