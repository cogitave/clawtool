# clawtool — multi-stage Docker build.
#
# Stage 1: build the Go binary with -trimpath + ldflags so the
# image carries no source paths and a sensible version string.
# Stage 2: copy the binary into distroless/static — no shell, no
# package manager, no glibc, just clawtool + ca-certificates.
#
# Why distroless/static?
#   - 6-7 MB final image (vs ~50 MB alpine, ~80 MB debian-slim).
#   - No shell → no in-container exec attack surface.
#   - Static binary works because Go produces one when CGO_ENABLED=0
#     and we don't pull modernc/sqlite's CGO path.
#
# Build:        docker build -t cogitave/clawtool:latest .
# Run (stdio):  docker run -i --rm cogitave/clawtool:latest serve
# Run (HTTP):   docker run -p 8080:8080 -v ~/.config/clawtool:/config \
#                 -e XDG_CONFIG_HOME=/ \
#                 cogitave/clawtool:latest \
#                 serve --listen :8080 --token-file /config/clawtool/listener-token

# ─── stage 1: build ──────────────────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads in their own layer so source-only edits
# don't bust the dep cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Embed version metadata via -X if internal/version exposes the
# variables. Static build (CGO_ENABLED=0) so distroless/static
# can run the result without libc.
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags="-s -w \
          -X github.com/cogitave/clawtool/internal/version.Version=${VERSION} \
          -X github.com/cogitave/clawtool/internal/version.Commit=${COMMIT} \
          -X github.com/cogitave/clawtool/internal/version.BuildDate=${BUILD_DATE}" \
        -o /out/clawtool ./cmd/clawtool

# ─── stage 2: runtime ────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

# OCI labels for registries that surface them (ghcr, docker hub).
LABEL org.opencontainers.image.title="clawtool"
LABEL org.opencontainers.image.description="MCP server + dispatch layer for AI coding agents."
LABEL org.opencontainers.image.source="https://github.com/cogitave/clawtool"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=build /out/clawtool /usr/local/bin/clawtool

# distroless/static-nonroot runs as UID 65532. Mount user configs
# read-only at /config when running serve.
USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/clawtool"]
CMD ["serve"]
