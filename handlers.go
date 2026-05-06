package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// trustedProxies holds the parsed CIDR list of reverse proxies whose
// X-Real-IP / X-Forwarded-For headers are trustworthy. Empty means
// direct-exposure mode — proxy headers are ignored entirely so they
// cannot be spoofed by arbitrary clients.
var trustedProxies []netip.Prefix

func homeHandler(w http.ResponseWriter, r *http.Request) {

	if !methodAllowed(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	addr := clientIP(r)
	if !addr.IsValid() {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	s := addr.String()
	if r.Method == http.MethodHead {
		// HEAD: announce the body length but write nothing. For GET,
		// net/http auto-sets Content-Length from the body — keeping
		// strconv.Itoa off the GET hot path saves one allocation.
		w.Header().Set("Content-Length", strconv.Itoa(len(s)))
		return
	}

	// #nosec G705 -- addr was strictly validated via netip.ParseAddr;
	// response is text/plain with X-Content-Type-Options: nosniff.
	_, _ = io.WriteString(w, s)
}

// clientIP resolves the client's IP address. Resolution depends on
// trustedProxies:
//
//	Empty trustedProxies → return the parsed RemoteAddr (proxy
//	headers are ignored so external clients cannot spoof them).
//
//	Non-empty → if RemoteAddr falls inside one of the prefixes,
//	consult X-Real-IP then X-Forwarded-For (leftmost). Otherwise
//	return the parsed RemoteAddr.
//
// Each header candidate is validated through netip.ParseAddr; invalid
// candidates fall through to the next source. Returns the zero
// netip.Addr when no valid IP can be determined.
func clientIP(r *http.Request) netip.Addr {
	// netip.ParseAddrPort handles both "host:port" and "[ipv6]:port"
	// without the per-call string allocation that net.SplitHostPort
	// incurs — preserving the zero-alloc hot path under load.
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		slog.Error("Request", "Error", err.Error())
		return netip.Addr{}
	}
	remote := ap.Addr()

	if !remoteIsTrusted(remote) {
		return remote
	}

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

	return remote
}

// remoteIsTrusted reports whether addr falls inside any of the
// configured trusted-proxy prefixes.
func remoteIsTrusted(addr netip.Addr) bool {
	if !addr.IsValid() || len(trustedProxies) == 0 {
		return false
	}
	for _, p := range trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseTrustedProxies parses a comma-separated list of CIDRs into
// []netip.Prefix. Empty strings (including whitespace-only entries)
// are skipped. Any invalid CIDR returns an error naming the offender.
func parseTrustedProxies(s string) ([]netip.Prefix, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", p, err)
		}
		out = append(out, prefix)
	}
	return out, nil
}
