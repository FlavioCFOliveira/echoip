package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
)

var HOST string
var PORT int
var TLSCert string     // ECHOIP_TLS_CERT — empty means plain HTTP
var TLSKey string      // ECHOIP_TLS_KEY  — empty means plain HTTP
var ProxyProtocol bool // ECHOIP_PROXY_PROTOCOL=true wraps the listener
var MaxConns int       // ECHOIP_MAX_CONNS, 0 disables the connection cap
var RateLimit int      // ECHOIP_RATE_LIMIT requests/min per client IP, 0 disables

// initConfig reads every ECHOIP_* env var into package-level globals
// and exits the process on any validation error. Called from main()
// rather than a package init() so tests can run without any env
// dependency and override the globals directly.
func initConfig() {

	// initializing the logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if HOST = os.Getenv("ECHOIP_HOST"); HOST == "" {
		HOST = "0.0.0.0"
	}
	if err := validateHost(HOST); err != nil {
		slog.Error("Invalid ECHOIP_HOST", "host", HOST, "Error", err.Error())
		os.Exit(1)
	}

	PORT = 8080 // default port
	if portStr := os.Getenv("ECHOIP_PORT"); portStr != "" {
		portint, err := strconv.Atoi(portStr)
		if err != nil {
			slog.Error("Invalid ECHOIP_PORT (not an integer)", "value", portStr, "Error", err.Error())
			os.Exit(1)
		}
		PORT = portint
	}
	if err := validatePort(PORT); err != nil {
		slog.Error("Invalid ECHOIP_PORT", "port", PORT, "Error", err.Error())
		os.Exit(1)
	}

	if proxies := os.Getenv("ECHOIP_TRUSTED_PROXIES"); proxies != "" {
		parsed, err := parseTrustedProxies(proxies)
		if err != nil {
			slog.Error("Invalid ECHOIP_TRUSTED_PROXIES", "Error", err.Error())
			os.Exit(1)
		}
		trustedProxies = parsed
	}

	TLSCert = os.Getenv("ECHOIP_TLS_CERT")
	TLSKey = os.Getenv("ECHOIP_TLS_KEY")
	if (TLSCert == "") != (TLSKey == "") {
		slog.Error("ECHOIP_TLS_CERT and ECHOIP_TLS_KEY must be set together (or both empty)")
		os.Exit(1)
	}

	ProxyProtocol = os.Getenv("ECHOIP_PROXY_PROTOCOL") == "true"

	MaxConns = 10000
	if s := os.Getenv("ECHOIP_MAX_CONNS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			slog.Error("Invalid ECHOIP_MAX_CONNS", "value", s)
			os.Exit(1)
		}
		MaxConns = n
	}

	RateLimit = 60
	if s := os.Getenv("ECHOIP_RATE_LIMIT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			slog.Error("Invalid ECHOIP_RATE_LIMIT", "value", s)
			os.Exit(1)
		}
		RateLimit = n
	}
}

func validatePort(p int) error {
	if p < 1 || p > 65535 {
		return errors.New("must be in 1..65535")
	}
	return nil
}

// validateHost accepts the empty string (caller defaults to 0.0.0.0),
// any literal IPv4/IPv6 address, or a plausible hostname (no control
// characters or whitespace, length up to RFC 1035's 253 octets). Real
// DNS resolution is deferred to net.Listen so that startup stays
// offline-friendly.
func validateHost(h string) error {
	if h == "" {
		return nil
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return nil
	}
	if len(h) > 253 {
		return fmt.Errorf("hostname too long (%d > 253)", len(h))
	}
	for _, c := range h {
		if c <= ' ' || c == 0x7F {
			return fmt.Errorf("contains invalid character %q", c)
		}
	}
	return nil
}
