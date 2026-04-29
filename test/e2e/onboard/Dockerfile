# test/e2e/onboard/Dockerfile — host fixture for clawtool onboard --yes.
#
# Bakes a tiny set of mock host CLIs (claude/codex/gemini) on PATH
# so `clawtool onboard --yes` exercises detect → primary-CLI default
# → bridge-install → MCP-claim → daemon-start → identity → secrets
# → marker, all without external network. Each mock binary is a
# 5-line bash stub that records its argv to /tmp/<name>.invocations
# so the harness can assert what onboard called and how.
#
# Usage:
#   docker build -f test/e2e/onboard/Dockerfile -t clawtool-e2e-onboard:dev .
#   docker run --rm clawtool-e2e-onboard:dev /usr/local/bin/run.sh
#
# Stages: build (golang → /usr/local/bin/clawtool) + run (slim
# distro with the binary + mocks + harness script).

# ── build stage ─────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/clawtool ./cmd/clawtool

# ── run stage ───────────────────────────────────────────────────────
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates jq \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/clawtool /usr/local/bin/clawtool

# Mock CLIs. Each one prints a recognisable line on `--version` (so
# clawtool's detection layer can probe them) and appends every
# invocation to /tmp/<name>.invocations so the harness can prove
# what got called. The mocks intentionally do NOT implement `mcp
# add` etc. — onboard's claimMCPHost dial path will be exercised at
# the daemon level, not the host CLI level, so unrecognised subcmds
# would only matter if the harness asserted on bridge-install
# success. We accept anything and exit 0.
# Each mock binary is materialised by a Docker BuildKit heredoc-RUN.
# The pre-fix shape (a single RUN that nested a shell heredoc inside
# a `for ... do ; \` line continuation) was a Dockerfile parse
# error: BuildKit's heredoc terminator `EOF` collided with the
# shell's `\`-continuation, so the build never made it past
# `unknown instruction: chmod`. Modern shape: one heredoc-RUN per
# mock, tmpl substitution done with sed inside the script body.
RUN <<'BUILDMOCK'
set -eux
mkmock() {
    local name="$1"
    cat > "/usr/local/bin/${name}" <<MOCK
#!/usr/bin/env bash
echo "\$0 \$@" >> "/tmp/${name}.invocations"
case "\$1" in
    --version|-v|version) echo "${name} mock 0.0.1-e2e" ;;
    *) ;;
esac
exit 0
MOCK
    chmod +x "/usr/local/bin/${name}"
}
mkmock claude
mkmock codex
mkmock gemini
BUILDMOCK

COPY test/e2e/onboard/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/run.sh

# Run as a non-root user so the XDG paths exercise the same
# permission surface real users hit. NOTE: we deliberately pick
# `clawuser` rather than `operator` — the latter is reserved by
# Debian's base-files (UID 11, System V legacy), so `useradd
# operator` exits 9 ("username already in use") on bookworm-slim.
RUN useradd -m -s /bin/bash clawuser
USER clawuser
ENV HOME=/home/clawuser
WORKDIR /home/clawuser

ENTRYPOINT ["/usr/local/bin/run.sh"]
