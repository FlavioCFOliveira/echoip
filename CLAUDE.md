# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
gofmt -l .                                                            # CI fails if this prints any filenames
go mod tidy -diff                                                     # CI fails if this reports a diff
golangci-lint run ./...                                               # meta-linter (config in .golangci.yml)
govulncheck ./...                                                     # vulnerability scan against vuln.go.dev
```

Tooling install (once per machine):

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
```

There is currently no test file in the repo; `go test ./...` succeeds with no tests run. The `-race` and `-shuffle=on` flags surface order-dependent and concurrency bugs as soon as tests are added — keep them on.

Configuration is via environment variables, both optional:
- `ECHOIP_HOST` (default `0.0.0.0`)
- `ECHOIP_PORT` (default `8080`; must parse as int — invalid values cause `os.Exit(1)` at startup)

VS Code launch configurations in `.vscode/launch.json` provide "Launch Default" and "Launch Custom ENV" (binds to `127.0.0.1:8081`).

## Architecture

Three files, all `package main`:

- `init.go` — `init()` sets up the slog JSON logger, reads env vars into the package-level `HOST` and `PORT`, and registers the `/` route. Route registration happens here, not in `main`.
- `main.go` — builds an `&http.Server{}` with explicit `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` (the bare `http.ListenAndServe` is unsafe for public exposure — Slowloris). Add routes in `init.go`, not here.
- `handlers.go` — `homeHandler` delegates to `clientIP(r)`, which resolves the client IP with this precedence:
  1. `X-Real-IP` header
  2. `X-Forwarded-For` header (leftmost entry of the proxy chain)
  3. `r.RemoteAddr` (split with `net.SplitHostPort`)

  Each candidate is validated through `netip.ParseAddr`; invalid candidates fall through to the next source. The handler returns the canonical `netip.Addr.String()` form. The response sets `Content-Type: text/plain; charset=utf-8` and `X-Content-Type-Options: nosniff` to defeat content-type sniffing.

The header-first precedence assumes deployment behind a trusted reverse proxy. In a direct-exposure context these headers are client-controlled and spoofable — keep that trust assumption in mind when modifying the handler.

## CI

`.github/workflows/go.yml` runs these independent jobs on every push and pull request — all must pass:

- **format-check** — `gofmt -l .`
- **check-dep-change** — `go mod tidy -diff`
- **lint** — `golangci-lint` v2.12.1 with `.golangci.yml`; subsumes `go vet`, `staticcheck`, `errcheck`, `unused`, `ineffassign`, plus `gosec`, `bodyclose`, `errorlint`, `noctx`, `perfsprint`, `prealloc`, `unconvert`, `unparam`, `wastedassign`, `misspell`, `nilerr`, `gocheckcompilerdirectives`, `intrange`, `copyloopvar`.
- **vuln** — `govulncheck-action@v1` against `vuln.go.dev` (stdlib + dependencies).
- **test** — `go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic -v ./...`.
- **build** — `go build -trimpath -ldflags="-s -w" ./...` (reproducible, stripped).

The Go version is sourced from `go.mod` via `actions/setup-go@v6`'s `go-version-file` — bump it in one place. `.github/dependabot.yml` keeps Go modules and Action versions current weekly.

When adding a `// #nosec` directive, always include `--` followed by a justification — gosec waivers without rationale should be rejected in review.
