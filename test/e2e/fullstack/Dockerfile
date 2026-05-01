# test/e2e/fullstack/Dockerfile — end-to-end harness exercising the
# full v0.22.95–v0.22.106 surface in a single Ubuntu 24.04 container.
# Unlike test/e2e/bootstrap (which stubs `claude` for the bootstrap
# verb only) and test/e2e/realinstall (which exercises install.sh
# downloading the GitHub release), this fixture wires up:
#
#   - REAL clawtool binary (built from /src by the build stage)
#   - REAL daemon process (clawtool daemon start)
#   - REAL tmux server hosting REAL panes for stub agents
#   - REAL peer-register hooks calling the daemon's HTTP API
#   - REAL peer-send pushing into a REAL tmux send-keys pipeline
#
# The only stubs are the agent processes themselves (bash scripts
# under stub_agents/) — they exist because the LLM round-trip is not
# what we're testing here; the transport is. Each stub reads stdin
# from its tmux pane and writes a marker file the harness asserts on.
#
# Why ubuntu:24.04 (vs the other fixtures' alpine/debian-slim):
#   - the operator brief explicitly calls for ubuntu;
#   - install.sh's PATH detection / ~/.local/bin convention matches
#     a fresh Ubuntu user's expectations bit-for-bit;
#   - tmux + nodejs availability under apt is fast and predictable.
#
# Usage (manual):
#   docker build -f test/e2e/fullstack/Dockerfile -t clawtool-e2e-fullstack:dev .
#   docker run --rm clawtool-e2e-fullstack:dev
#
# Driver (CI):
#   go test -tags=e2e ./test/e2e/fullstack/...

# ── build stage ─────────────────────────────────────────────────────
# Same go version + CGO_ENABLED=0 link shape goreleaser uses, so the
# binary the harness exercises is binary-compatible with what users
# install via install.sh.
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Inject version via -ldflags to mirror goreleaser. Without this the
# binary reports the dev-fallback "0.21.7" (release-please sentinel)
# and harnesses that key on the version string see a stale value.
ARG CLAWTOOL_VERSION=dev-e2e
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X github.com/cogitave/clawtool/internal/version.Version=${CLAWTOOL_VERSION}" \
    -o /out/clawtool ./cmd/clawtool

# ── run stage ───────────────────────────────────────────────────────
FROM ubuntu:24.04

# tmux: the load-bearing dependency — peer send's send-keys path
# requires a real tmux server. nodejs: future-proofs the image for
# npm-based agent CLIs (codex / gemini are npm packages); installing
# now keeps the harness ready when the install path expands. bash
# is explicit because the stub_agents scripts use bashisms (read -t,
# >>, etc.) that ash/dash don't share. curl + ca-certificates are
# for any future install.sh roundtrips inside the harness.
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        jq \
        nodejs \
        procps \
        socat \
        tmux \
    && rm -rf /var/lib/apt/lists/*
# socat is still required by lifecycle scenarios A (window cleanup)
# and B (grace period) which dial the daemon's Unix dispatch socket
# directly via run.sh's dispatch_submit helper — they pass
# `mode:auto-tmux` only, no auto_close override, but the socket is
# still the simplest way to drive an async dispatch with a custom
# routing mode in a shell harness. Lifecycle scenario C (per-task
# auto_close override) was migrated to the native
# `clawtool send --no-auto-close` flag in v0.22.113 so the operator-
# visible CLI surface is exercised end-to-end. Once `--mode` is
# similarly absorbed into a first-class CLI flow on every dispatch
# socket call this image makes, socat can be dropped entirely.

# Built clawtool binary
COPY --from=build /out/clawtool /usr/local/bin/clawtool

# Stub agent loop + driver script
COPY test/e2e/fullstack/stub_agents/agent_loop.sh /usr/local/bin/agent_loop.sh
COPY test/e2e/fullstack/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/agent_loop.sh /usr/local/bin/run.sh

# Stub `claude` and `codex` binaries on PATH so any clawtool code
# path that probes for an agent CLI (clawtool agents detect, etc.)
# finds something. These stubs only handle --version probes; the
# actual agent loop runs via agent_loop.sh inside tmux panes.
RUN <<'STUBS'
set -eux
for name in claude codex gemini opencode; do
    cat > /usr/local/bin/${name} <<STUB
#!/usr/bin/env bash
# Stub ${name} CLI for the fullstack e2e fixture. Handles --version
# probes only; the real agent loop runs via agent_loop.sh in tmux.
case "\${1:-}" in
    --version|-v|version)
        echo "${name} stub 0.0.1-fullstack-e2e"
        exit 0
        ;;
esac
exec /usr/local/bin/agent_loop.sh "${name}-stub"
STUB
    chmod +x /usr/local/bin/${name}
done
STUBS

# Non-root operator user — same convention as the bootstrap /
# realinstall fixtures so the XDG paths + permissions exercise the
# same surface real installs hit.
RUN useradd -m -s /bin/bash clawuser \
    && install -d -m 1777 /tmp
USER clawuser
ENV HOME=/home/clawuser
ENV XDG_CONFIG_HOME=/home/clawuser/.config
ENV XDG_STATE_HOME=/home/clawuser/.local/state
ENV XDG_DATA_HOME=/home/clawuser/.local/share
ENV PATH=/home/clawuser/.local/bin:/usr/local/bin:/usr/bin:/bin
WORKDIR /home/clawuser

ENTRYPOINT ["/usr/local/bin/run.sh"]
