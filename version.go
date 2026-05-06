package main

import (
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
)

// Build metadata. Set by the build via:
//
//	go build -ldflags="-X main.version=v0.1.0 -X main.commit=abc123 -X main.date=2026-05-06"
//
// When ldflags are not set, runtime/debug.ReadBuildInfo fills in
// commit and date from the Go toolchain's embedded VCS info.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func init() {
	if commit != "unknown" && date != "unknown" {
		return
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "unknown" {
				commit = s.Value
			}
		case "vcs.time":
			if date == "unknown" {
				date = s.Value
			}
		}
	}
}

// versionHandler returns build metadata as text/plain. Kept tiny —
// no JSON dep, no allocation pool, the response is a couple of
// hundred bytes at most.
func versionHandler(w http.ResponseWriter, r *http.Request) {
	if !methodAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, "version: "+version+"\n")
	_, _ = io.WriteString(w, "commit:  "+commit+"\n")
	_, _ = io.WriteString(w, "date:    "+date+"\n")
	_, _ = io.WriteString(w, "go:      "+runtime.Version()+"\n")
}
