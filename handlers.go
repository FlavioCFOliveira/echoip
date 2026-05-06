package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func homeHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Server", "Echo Server 1.0")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	addr := clientIP(r)
	if !addr.IsValid() {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// #nosec G705 -- addr was strictly validated via netip.ParseAddr;
	// response is text/plain with X-Content-Type-Options: nosniff.
	_, _ = io.WriteString(w, addr.String())
}

// clientIP resolves the client's IP address with this precedence:
//  1. X-Real-IP header
//  2. X-Forwarded-For header (leftmost entry of the proxy chain)
//  3. TCP RemoteAddr
//
// Each candidate is validated through netip.ParseAddr; invalid candidates
// fall through to the next source. Returns the zero netip.Addr when no
// valid IP can be determined.
func clientIP(r *http.Request) netip.Addr {
	// Direct map access with canonical keys bypasses the per-call key
	// canonicalization (and its allocation) inside http.Header.Get. Safe
	// because net/http always stores keys in canonical form on parse.
	if v := r.Header["X-Real-Ip"]; len(v) > 0 && v[0] != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(v[0])); err == nil {
			return a
		}
	}

	if v := r.Header["X-Forwarded-For"]; len(v) > 0 && v[0] != "" {
		h := v[0]
		first := h
		if i := strings.IndexByte(h, ','); i >= 0 {
			first = h[:i]
		}
		if a, err := netip.ParseAddr(strings.TrimSpace(first)); err == nil {
			return a
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		slog.Error("Request", "Error", err.Error())
		return netip.Addr{}
	}
	a, _ := netip.ParseAddr(host)
	return a
}
