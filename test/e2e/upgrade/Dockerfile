# test/e2e/upgrade/Dockerfile — container fixture that validates the
# atomic-binary-swap → `clawtool daemon restart` flow end-to-end on a
# clean filesystem. Without this test we only know that the
# auto-recovery code path compiles + passes unit tests; we have no
# evidence that on a real machine `clawtool upgrade` (which calls
# the same daemon.Stop + daemon.Ensure pair) actually swings the
# running daemon onto the new binary.
#
# Two binaries are built at distinct ldflags-injected versions
# (v0.0.0-old, v0.0.0-new). The harness installs the old one,
# starts the daemon, asserts /v1/health reports v0.0.0-old, swaps
# the binary on disk (mimicking what selfupdate.UpdateTo does),
# runs `clawtool daemon restart`, and asserts /v1/health now
# reports v0.0.0-new. If the restart logic regressed, the test
# fails loudly instead of silently deferring breakage to release
# day.
#
# Usage:
#   docker build -f test/e2e/upgrade/Dockerfile -t clawtool-e2e-upgrade:dev .
#   docker run --rm clawtool-e2e-upgrade:dev
#
# Stages: build (golang → /out/clawtool-old + /out/clawtool-new) +
# run (slim distro with both binaries + harness script).

# ── build stage ─────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Two binaries with different version stamps. ldflags injection is
# the same mechanism GoReleaser uses on real release tarballs, so
# the test exercises the production version-resolution path
# (version.Resolved → ldflags) rather than a dev fallback.
RUN CGO_ENABLED=0 go build \
        -ldflags='-X github.com/cogitave/clawtool/internal/version.Version=v0.0.0-old' \
        -o /out/clawtool-old ./cmd/clawtool
RUN CGO_ENABLED=0 go build \
        -ldflags='-X github.com/cogitave/clawtool/internal/version.Version=v0.0.0-new' \
        -o /out/clawtool-new ./cmd/clawtool

# ── run stage ───────────────────────────────────────────────────────
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates jq curl procps \
    && rm -rf /var/lib/apt/lists/*

# Old binary is what the operator started with; new binary is what a
# release pipeline would publish. The harness atomically swaps the
# old one for the new one to simulate the post-UpdateTo state.
COPY --from=build /out/clawtool-old /usr/local/bin/clawtool
COPY --from=build /out/clawtool-new /opt/clawtool-new
COPY test/e2e/upgrade/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/clawtool /opt/clawtool-new /usr/local/bin/run.sh

WORKDIR /work
CMD ["/usr/local/bin/run.sh"]
