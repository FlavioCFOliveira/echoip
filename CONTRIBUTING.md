# Contributing

Contributions are welcome — bug fixes, performance work, new features, and documentation improvements.

## Before opening a PR

All of the following must be green locally:

```bash
gofmt -l .                                                              # must print nothing
go mod tidy -diff                                                       # must report no diff
golangci-lint run ./...                                                 # must report 0 issues
govulncheck ./...                                                       # must find no vulnerabilities
go test -race -shuffle=on -coverprofile=coverage.out -covermode=atomic ./...
go build -trimpath -ldflags="-s -w" ./...                               # production-style build
```

CI runs the same set on every push and pull request — see `.github/workflows/go.yml`.

## Performance posture

`echoip` targets very high concurrency. The handler hot path is allocation-light and benchmarks gate regressions.

For any change that touches the request path:

1. Run benchmarks before and after:
   ```bash
   go test -bench=. -benchmem -count=10 ./...
   ```
2. Use `benchstat` to compare baselines.
3. Include the numbers in the PR description — `ns/op`, `B/op`, `allocs/op` for both states.

See `CLAUDE.md` for the full performance philosophy and tooling reference.

## Tests

- Unit tests live alongside the code they cover (`*_test.go`).
- `handlers.go` and `health.go` are at 100% coverage; new code in those files is expected to maintain that.
- Use the existing `scenario` table when extending `clientIP` / `homeHandler` tests so behaviour stays cross-checked between unit and benchmark runs.

## Commit messages

- Imperative mood (`Add`, `Fix`, `Refactor`).
- One subject line under ~70 characters.
- A body that explains *why* — what the change buys, not just what it does.
- For roadmap-tracked work, end with `Closes roadmap task #N.`

## Style

- Idiomatic Go. `gofmt`/`goimports` conventions; no aliases of `time`, `context`, etc.
- Prefer `net/http` primitives directly over wrapper abstractions.
- New third-party dependencies require justification — the zero-dependency posture is intentional.

## Reporting security issues

See [`SECURITY.md`](SECURITY.md). Please do not open public issues for vulnerabilities.
