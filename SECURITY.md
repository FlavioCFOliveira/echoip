# Security Policy

## Reporting a vulnerability

Please **do not** open a public GitHub issue for vulnerabilities. Instead, use GitHub's private reporting:

1. Go to <https://github.com/FlavioCFOliveira/echoip/security/advisories/new>
2. Describe the issue, the affected version, and a reproducer if possible.
3. We aim to acknowledge within 72 hours and to publish a fix or mitigation as soon as the impact and remediation are understood.

If GitHub Security Advisories is not an option, email the maintainer at the address shown on the GitHub profile.

## Supported versions

Only the latest commit on `main` is supported. Tagged releases will be added once the project ships its first release pipeline (see roadmap).

## Hardening posture

- Strict timeouts on the HTTP server defeat slow-read attacks.
- Every IP candidate is validated through `net/netip` before responding.
- Responses set `X-Content-Type-Options: nosniff`.
- The `Server` response header is intentionally omitted.
- `govulncheck` runs in CI on every change against `vuln.go.dev`.
- The default trust model ignores `X-Real-IP` / `X-Forwarded-For` unless `ECHOIP_TRUSTED_PROXIES` is set, preventing spoofing in direct-exposure deployments.
