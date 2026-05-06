# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Roadmap

**Name:** echoip

## Project

`echoip` is a small HTTP service that returns the requesting client's public IP address as `text/plain`. It is deployed at https://echo-ip.com. Go 1.26.2, standard library only — no third-party dependencies.

## Performance posture

This service targets very high concurrency and is expected to hold up under heavy load and stress. Every change on the request path is evaluated first on responsiveness and throughput — performance is a load-bearing design constraint, not an afterthought.

- Keep the handler hot path allocation-light: avoid unnecessary per-request allocations, copies, or string formatting.
- Do not introduce blocking work, global locks, or shared mutable state on the request path. Defer heavy work off the hot path.
- New dependencies must justify their cost under load; the current zero-dependency posture is intentional.
- Prefer `net/http` primitives directly over wrapper abstractions that add indirection.

Use intuition to direct both the implementation and the tests/benchmarks that validate it. Draw on a broad pool of knowledge — data structures, algorithms, design patterns, CPU and memory-hierarchy behaviour (cache lines, false sharing, branch prediction, NUMA), garbage collection and memory management, and the high-performance strategies developed across C, C++, Rust, Java, and other systems ecosystems — and transpose them into idiomatic Go: escape-analysis-aware code, `sync.Pool` for hot-path reuse, lock-free / sharded structures, cache-friendly layouts, batching, zero-copy I/O, GC-pressure minimization, and so on. Intuition chooses the experiments and what to measure; measurement gates acceptance. Only solutions that are demonstrably **faster and at least as safe** are accepted, however compelling the idea looked beforehand. Capture numbers with the Go toolchain and standard load tooling:

- **Benchmarks**: `go test -bench=. -benchmem -count=10 ./...`; compare baseline vs. change with `benchstat`.
- **Profiling**: `pprof` for CPU, heap, goroutine, block, and mutex profiles — via `go test -cpuprofile=cpu.out -memprofile=mem.out` for benchmarks, or `net/http/pprof` exposed temporarily on a running instance.
- **Concurrency correctness**: `go test -race`.
- **Runtime visibility**: `GODEBUG=gctrace=1`, `runtime/trace`, and pprof's allocs view for GC pressure.
- **End-to-end load**: `wrk`, `hey`, `vegeta`, or `bombardier` for throughput and latency under concurrency.

Record methodology and baseline-vs-after numbers in the PR description so reviewers can reproduce.

## Commands

```bash
go run .                                                              # run locally (binds 0.0.0.0:8080 by default)
go build -trimpath -ldflags="-s -w" ./...                             # production-style build (CI uses these flags)
go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic ./...   # tests as CI runs them
go test -bench=. -benchmem -count=10 ./...                            # benchmarks
go test -fuzz=FuzzSomething -fuzztime=30s ./...                       # fuzzing (when fuzz tests exist)
gofmt -s -l .                                                         # CI fails if this prints any filenames (-s applies simplifications)
go mod tidy -diff                                                     # CI fails if this reports a diff
golangci-lint run ./...                                               # meta-linter (config in .golangci.yml)
govulncheck ./...                                                     # vulnerability scan against vuln.go.dev
```

Tooling install (once per machine):

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
```

Tests live in `*_test.go` alongside the code they cover. `handlers.go`, `health.go`, and `proxyproto.go` are at 100% coverage; `limit.go` is fully exercised by table-driven and concurrency tests. The `-race` and `-shuffle=on` flags catch order-dependent and concurrency bugs — keep them on.

Configuration is via environment variables, all optional:
- `ECHOIP_HOST` (default `0.0.0.0`) — validated as IP literal or plausible hostname.
- `ECHOIP_PORT` (default `8080`) — must be `1..65535`.
- `ECHOIP_TRUSTED_PROXIES` — comma-separated CIDR list. Empty (default) = direct-exposure mode = `X-Real-IP` / `X-Forwarded-For` ignored. Invalid CIDR fails startup.
- `ECHOIP_TLS_CERT` / `ECHOIP_TLS_KEY` — pair of PEM file paths. Set together to serve TLS (HTTP/2). Setting only one fails startup.
- `ECHOIP_PROXY_PROTOCOL` — `true` to enable PROXY v1/v2 listener decoder.
- `ECHOIP_MAX_CONNS` (default `10000`) — global cap on simultaneously accepted connections; `0` disables.
- `ECHOIP_RATE_LIMIT` (default `60`) — per-IP token-bucket requests/minute on `/`; `0` disables. Health endpoints are exempt.

VS Code launch configurations in `.vscode/launch.json` provide "Launch Default" and "Launch Custom ENV" (binds to `127.0.0.1:8081`).

## Architecture

`package main` only:

- `init.go` — sets up the slog JSON logger and parses every env var into package-level globals (`HOST`, `PORT`, `TLSCert`, `TLSKey`, `ProxyProtocol`, `MaxConns`, `RateLimit`, `trustedProxies`). Pure-function validators (`validatePort`, `validateHost`, `parseTrustedProxies`) are unit-tested.
- `main.go` — binds the listener (`(*net.ListenConfig).Listen` for ctx propagation), wraps it with the optional PROXY protocol decoder and connection limiter, builds the `*http.ServeMux` via `routes(rl)`, and hands off to `run(ctx, server, ln, certFile, keyFile)`. `run` serves until the context is cancelled (SIGTERM/SIGINT) and then drains via `server.Shutdown` with a 30s deadline. `/readyz` flips to 503 the moment shutdown begins.
- `handlers.go` — `homeHandler` accepts only GET/HEAD (others get 405 + `Allow`); delegates to `clientIP(r)`. `clientIP` parses `r.RemoteAddr` once via `netip.ParseAddrPort` (zero-alloc), then if the parsed address falls inside one of `trustedProxies` it consults `X-Real-IP` then `X-Forwarded-For` (leftmost). Empty `trustedProxies` = headers ignored. Response is `text/plain; charset=utf-8` with `X-Content-Type-Options: nosniff`; `Server` is intentionally absent.
- `health.go` — `/healthz`, `/livez`, `/readyz` handlers + the shared `methodAllowed` and `writeOK` helpers. `readyzHandler` is gated by an `atomic.Bool` (`ready`) flipped to true in `main` once the listener is open.
- `limit.go` — `connLimitListener` (semaphore-backed listener) and `rateLimiter` (token bucket per `netip.Addr` in a `sync.Map` with periodic eviction).
- `proxyproto.go` — in-house PROXY v1 (text) and v2 (binary, AF_INET/AF_INET6) decoder. Mode is "require": connections without a header are dropped with a slog WARN.

The header-first precedence inside the trust gate still assumes the listed CIDRs are actually upstream of the deployment. Strip or overwrite proxy headers at your trust boundary so external clients cannot reach the service with forged values.

## CI

`.github/workflows/go.yml` runs these independent jobs on every push and pull request — all must pass:

- **format-check** — `gofmt -s -l .`
- **check-dep-change** — `go mod tidy -diff`
- **lint** — `golangci-lint` v2.12.1 with `.golangci.yml`; subsumes `go vet`, `staticcheck`, `errcheck`, `unused`, `ineffassign`, plus `gosec`, `bodyclose`, `errorlint`, `noctx`, `perfsprint`, `prealloc`, `unconvert`, `unparam`, `wastedassign`, `misspell`, `nilerr`, `gocheckcompilerdirectives`, `intrange`, `copyloopvar`.
- **vuln** — `govulncheck-action@v1` against `vuln.go.dev` (stdlib + dependencies).
- **test** — `go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic -v ./...`.
- **build** — `go build -trimpath -ldflags="-s -w" ./...` (reproducible, stripped).

The Go version is sourced from `go.mod` via `actions/setup-go@v6`'s `go-version-file` — bump it in one place. `.github/dependabot.yml` keeps Go modules and Action versions current weekly.

When adding a `// #nosec` directive, always include `--` followed by a justification — gosec waivers without rationale should be rejected in review.
