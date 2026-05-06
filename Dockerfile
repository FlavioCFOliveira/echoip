# syntax=docker/dockerfile:1
#
# Multi-stage build for echoip. Produces a ~5 MB scratch-equivalent
# image based on distroless/static, running as non-root uid 65532.

# ----------------------------- builder -----------------------------
FROM golang:1.26.2-alpine AS builder

WORKDIR /src

# Copy module files first so dependency download is cached separately
# from source changes. echoip currently has zero third-party deps —
# this still works (go mod download is a no-op).
COPY go.mod ./
RUN go mod download

COPY *.go ./

# Static, stripped, reproducible — same flags as CI build job.
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/echoip \
    .

# ----------------------------- runtime -----------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/echoip /echoip

# nonroot tag pins uid/gid 65532. EXPOSE is documentary — the listener
# binds to whatever ECHOIP_PORT (default 8080) resolves to at runtime.
USER 65532:65532
EXPOSE 8080

# No HEALTHCHECK directive: distroless/static has neither shell nor
# wget/curl, so Docker-level healthchecks would require shipping a
# probe binary. Orchestrators should hit /healthz, /livez, /readyz
# over HTTP directly — those endpoints are the supported surface.

ENTRYPOINT ["/echoip"]
