# echoip — Public IP Echo Service

A high-performance, zero-dependency HTTP service that returns the requesting client's public IP address as plain text. Written in Go, deployed at **[echo-ip.com](https://echo-ip.com)**, and engineered for very high concurrency under sustained load.

[![CI Go](https://github.com/FlavioCFOliveira/echoip/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/FlavioCFOliveira/echoip/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/FlavioCFOliveira/echoip/branch/main/graph/badge.svg)](https://codecov.io/gh/FlavioCFOliveira/echoip)
[![Go Version](https://img.shields.io/github/go-mod/go-version/FlavioCFOliveira/echoip)](https://go.dev/)
[![License: MIT](https://img.shields.io/github/license/FlavioCFOliveira/echoip)](LICENSE)

## What is echoip?

`echoip` answers a single question — **"What is my public IP address?"** — and returns the answer as one line of `text/plain`. It is the kind of small, reliable building block that shell scripts, CI pipelines, IoT devices, dynamic-DNS clients, network-diagnostic tools, and infrastructure automation reach for when they need to discover the egress IP of the machine running them.

The hosted instance is **free**, requires **no API key**, **no sign-up**, and imposes **no rate-limit headers** to negotiate. The source is **MIT-licensed** and trivially self-hostable.

```bash
curl https://echo-ip.com
# → 203.0.113.42
```

## Quick start

### Use the hosted service

```bash
# Plain HTTP request — returns text/plain, no trailing newline
curl https://echo-ip.com

# Capture into a shell variable
MY_IP=$(curl -s https://echo-ip.com)
echo "Public IP: $MY_IP"

# wget alternative
wget -qO- https://echo-ip.com
```

### Self-host

```bash
git clone https://github.com/FlavioCFOliveira/echoip.git
cd echoip
go run .                                # binds 0.0.0.0:8080 by default
```

Override host or port with environment variables:

```bash
ECHOIP_HOST=127.0.0.1 ECHOIP_PORT=9000 go run .
```

Production-style build (matches CI):

```bash
go build -trimpath -ldflags="-s -w" ./...
```

## Why echoip?

- **Zero third-party dependencies.** Pure Go standard library. No `go.sum`, nothing to audit beyond the language itself.
- **Allocation-light hot path.** The handler avoids per-request allocations where possible — direct map access against canonical header keys bypasses the per-call canonicalisation (and its allocation) inside `http.Header.Get`. A benchmark suite (`go test -bench=. -benchmem`) gates regressions.
- **Hardened for public exposure.** Explicit `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` defeat Slowloris-style attacks that the bare `http.ListenAndServe` is vulnerable to.
- **Content-sniffing defeated.** Every response sets `Content-Type: text/plain; charset=utf-8` and `X-Content-Type-Options: nosniff`.
- **Reverse-proxy aware.** Honours `X-Real-IP` and `X-Forwarded-For` (leftmost entry) when deployed behind trusted proxies, with strict `netip.ParseAddr` validation at every step.
- **Reproducible builds.** CI builds with `-trimpath -ldflags="-s -w"` for stripped, path-independent binaries.
- **IPv4 and IPv6 first-class.** Both families are handled identically and returned in canonical form.

## API reference

### `GET /`

Returns the resolved public IP address of the requesting client.

| Field | Value |
|-------|-------|
| **Method** | `GET` |
| **Path** | `/` |
| **Status (success)** | `200 OK` |
| **Status (failure)** | `500 Internal Server Error` |
| **Content-Type** | `text/plain; charset=utf-8` |
| **Body** | One line, e.g. `203.0.113.42` or `2001:db8::1`. No trailing newline. |
| **Auth** | None |
| **Rate limit** | None advertised |

### Health endpoints

For Kubernetes, load balancers, and uptime monitors. All return `text/plain` `ok` (or `not ready`) and accept GET/HEAD only.

| Path | Purpose | Notes |
|------|---------|-------|
| `GET /healthz` | Process is alive and accepting requests | Always 200 once the process is up. |
| `GET /livez` | Liveness — restart if it fails | Same as healthz today; reserved for future internal-degradation checks. |
| `GET /readyz` | Readiness — should receive traffic? | 200 once the HTTP server has bound; 503 during cold start or shutdown. |
| `GET /version` | Build metadata | `version`, `commit`, `date`, `go` lines as `text/plain`. |
| `GET /metrics` | Prometheus exposition | Counters (requests by class), gauge (in-flight), histogram (duration). |

### Configuration

Configuration is environment-variable based; both variables are optional.

| Variable | Default | Notes |
|----------|---------|-------|
| `ECHOIP_HOST` | `0.0.0.0` | Bind address. |
| `ECHOIP_PORT` | `8080` | TCP port. Must parse as int — invalid values cause `os.Exit(1)` at startup. |
| `ECHOIP_TRUSTED_PROXIES` | _(empty)_ | Comma-separated CIDR list of reverse-proxy networks whose `X-Real-IP` / `X-Forwarded-For` headers are trustworthy. Empty = direct-exposure mode = headers ignored. Invalid CIDR fails startup. |
| `ECHOIP_TLS_CERT` | _(empty)_ | Path to PEM-encoded certificate. If set together with `ECHOIP_TLS_KEY`, the listener serves TLS (and HTTP/2) instead of plain HTTP. Setting only one of the pair fails startup. |
| `ECHOIP_TLS_KEY` | _(empty)_ | Path to PEM-encoded private key — see `ECHOIP_TLS_CERT`. |
| `ECHOIP_PROXY_PROTOCOL` | _(empty)_ | Set to `true` to enable the in-house PROXY protocol v1/v2 listener decoder. Required when fronting echoip with an L4 LB (HAProxy, AWS NLB, GCP NLB) that does not inject HTTP headers. Connections without a valid PROXY header are dropped. |
| `ECHOIP_MAX_CONNS` | `10000` | Maximum simultaneous accepted connections. Excess Accepts block until a slot frees. `0` disables the cap. |
| `ECHOIP_RATE_LIMIT` | `60` | Per-client-IP token-bucket rate limit, in requests/minute. Burst equals the limit. Exceeding requests get `429` with `Retry-After`. `0` disables rate limiting. Health endpoints are exempt. |

## How it works

Three Go files, one responsibility each:

| File | Role |
|------|------|
| `init.go` | Sets up structured JSON logging (`log/slog`), reads `ECHOIP_HOST` / `ECHOIP_PORT`, registers the `/` route. |
| `main.go` | Constructs `&http.Server{}` with hardened timeouts and starts listening. |
| `handlers.go` | `homeHandler` resolves the client IP and writes it as plain text. |

**Client-IP resolution** is gated by `ECHOIP_TRUSTED_PROXIES`. The TCP `RemoteAddr` is parsed first via `netip.ParseAddrPort` (zero-allocation). Then:

- **If `RemoteAddr` falls inside one of the trusted-proxy prefixes**, `X-Real-IP` is consulted, then `X-Forwarded-For` (leftmost entry). Each header value is validated with `netip.ParseAddr`; invalid candidates fall through to the next source, and ultimately to the parsed `RemoteAddr`.
- **Otherwise** (or when the trusted-proxy list is empty), the parsed `RemoteAddr` is returned directly. Proxy headers are not consulted, so they cannot be spoofed by arbitrary clients.

The response body is the canonical `netip.Addr.String()` form.

> **Trust model.** The default — empty `ECHOIP_TRUSTED_PROXIES` — is direct-exposure-safe: spoofed `X-Real-IP` headers are ignored. To honour proxy headers in production, set `ECHOIP_TRUSTED_PROXIES` to the CIDR of every reverse proxy that fronts the service (e.g. `ECHOIP_TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12` for an internal LB). Strip or overwrite proxy headers at your trust boundary so external clients cannot reach the service with forged values.

## Use cases

- **Discovering egress IP from CI / shell scripts** — useful when an upstream allow-list requires the outbound IP of a runner or jump-box.
- **Dynamic DNS clients** detecting a changed WAN address.
- **Headless servers, containers, and IoT devices** that need to report their public address to a controller.
- **Network diagnostics** — comparing what an internet endpoint observes against `ip route` / `ifconfig` output.
- **Smoke-testing reverse-proxy configuration** — verifying whether `X-Forwarded-For` is being honoured end-to-end.
- **Security tooling** — confirming the egress IP that perimeter firewalls or SaaS allow-lists will see.

## Examples in other languages

### cURL
```bash
curl -s https://echo-ip.com
```

### Python
```python
import urllib.request
ip = urllib.request.urlopen("https://echo-ip.com").read().decode().strip()
print(ip)
```

### JavaScript (Node.js / browser)
```javascript
const ip = (await fetch("https://echo-ip.com").then(r => r.text())).trim();
console.log(ip);
```

### Go
```go
resp, _ := http.Get("https://echo-ip.com")
defer resp.Body.Close()
b, _ := io.ReadAll(resp.Body)
fmt.Println(string(b))
```

### PowerShell
```powershell
(Invoke-WebRequest -Uri "https://echo-ip.com").Content.Trim()
```

### Rust (with `reqwest`)
```rust
let ip = reqwest::blocking::get("https://echo-ip.com")?.text()?;
println!("{}", ip.trim());
```

## Performance

Performance is a load-bearing design constraint, not an afterthought. The repository ships with a benchmark suite covering the request hot path:

```bash
go test -bench=. -benchmem -count=10 ./...
```

Reproducible numbers should be captured with `benchstat` (baseline vs. change) and recorded in PR descriptions. Profiling helpers are wired in:

```bash
go test -bench=. -cpuprofile=cpu.out -memprofile=mem.out ./...
go tool pprof cpu.out
```

End-to-end load can be measured with `wrk`, `hey`, `vegeta`, or `bombardier`.

## Development

```bash
go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic ./...   # full test suite (CI flags)
go test -bench=. -benchmem -count=10 ./...                                     # benchmarks
gofmt -l .                                                                     # formatter check
go mod tidy -diff                                                              # dependency drift check
golangci-lint run ./...                                                        # full lint set
govulncheck ./...                                                              # vulnerability scan against vuln.go.dev
```

CI runs every job above on every push and pull request — see [`.github/workflows/go.yml`](.github/workflows/go.yml). The lint set includes `gosec`, `bodyclose`, `errorlint`, `noctx`, `perfsprint`, `prealloc`, `unconvert`, `unparam`, `wastedassign`, `misspell`, `nilerr`, `gocheckcompilerdirectives`, `intrange`, and `copyloopvar` on top of the standard `go vet`, `staticcheck`, `errcheck`, `unused`, `ineffassign` set.

## FAQ

### What does echoip return?
A single line of plain text containing the client's public IPv4 or IPv6 address. No JSON, no HTML, no trailing newline.

### Is there a JSON variant?
Not at present. The project's design constraint is responsiveness under load — adding response negotiation conflicts with that. Pipe `curl` output into `jq -Rn '{ip: input}'` if you need JSON.

### Is echoip free to use?
Yes. The hosted instance at https://echo-ip.com is free with no API key and no sign-up. The source is MIT-licensed; you can also self-host.

### Does echoip log my IP?
The service emits structured JSON via `log/slog` for operational purposes (errors, request handling). It does not run analytics on visitors and does not share data with third parties.

### Does it support IPv6?
Yes. IPv4 and IPv6 are handled identically — both are validated with `net/netip` and returned in canonical form.

### Does it work behind Cloudflare / nginx / Caddy / Traefik?
Yes. The service honours `X-Real-IP` and `X-Forwarded-For` (leftmost entry), so any reverse proxy that sets those headers will work. Strip or overwrite those headers at your trust boundary so external clients cannot spoof them.

### How is this different from `ifconfig.me`, `ipify`, or `icanhazip`?
`echoip` is open source, dependency-free, MIT-licensed, and built for self-hosting under heavy load. It is intentionally minimal — a single endpoint, a single response format. Use whichever fits your operational constraints.

### Why Go?
Static binary, mature `net/http` server, predictable garbage collector, no runtime to install. Deploys cleanly in containers, on bare metal, or behind any reverse proxy.

### Can I run it on Kubernetes / Docker / systemd?
Yes. It is a single static binary that respects `ECHOIP_HOST` and `ECHOIP_PORT`. Wrap it in any process manager or container image you prefer.

### Is there an SDK?
No SDK is needed — the response is plain text from a single HTTP `GET`. Any HTTP client in any language can consume it in one line.

## Security

If you discover a vulnerability, please open a private security advisory on GitHub rather than a public issue.

The service:
- Uses explicit timeouts (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`) to prevent slow-read attacks.
- Validates every IP candidate with `net/netip` before responding.
- Sets `X-Content-Type-Options: nosniff` to defeat MIME-sniffing.
- Runs `govulncheck` against `vuln.go.dev` in CI on every change.

## Contributing

Contributions are welcome. Before submitting a PR:

1. `gofmt -l .` must produce no output.
2. `golangci-lint run ./...` must pass.
3. `go test -race -shuffle=on ./...` must pass.
4. Performance-relevant changes must include benchmark numbers (baseline vs. change) in the PR description.

See [`CLAUDE.md`](CLAUDE.md) for the full performance and architecture posture used by automated agents on this codebase.

## License

[MIT](LICENSE) © Flávio CF Oliveira

## Links

- **Hosted service**: <https://echo-ip.com>
- **Source code**: <https://github.com/FlavioCFOliveira/echoip>
- **Issue tracker**: <https://github.com/FlavioCFOliveira/echoip/issues>
