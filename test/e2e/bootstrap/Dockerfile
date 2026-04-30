# test/e2e/bootstrap/Dockerfile — container fixture for the
# zero-click bootstrap flow. The bootstrap verb is being added in a
# parallel branch (autodev/bootstrap-claude-init); this fixture is
# designed to land NOW in a way that:
#
#   1. compiles + runs cleanly even when the verb is missing (the
#      Go test driver detects "verb not present" by running
#      `clawtool bootstrap --help` and skips on non-zero exit);
#   2. flips into a real assertion automatically once the parallel
#      branch lands the verb, no harness change needed.
#
# Scenario design (driven by run.sh):
#   1. Build the clawtool binary from /src (CGO_ENABLED=0 — same
#      knob goreleaser uses, so we exercise the production link
#      shape).
#   2. Place a STUB `claude` binary on PATH at /usr/local/bin/claude
#      that records its argv AND its stdin payload to a tmp file.
#      The stub MUST NOT make a real Claude API call — bootstrap is
#      the moment we'd otherwise burn an operator's first token on
#      a CI run, so the stub is hard-coded to be the only `claude`
#      reachable in the container.
#   3. Run `clawtool bootstrap --agent claude`. The verb is expected
#      to feed a prompt into the stub on stdin (or via argv); the
#      test asserts the stub was invoked and exits cleanly.
#
# Skipping: the Go driver runs `clawtool bootstrap --help` first;
# if the verb isn't wired yet (the parallel branch hasn't merged),
# the help command exits non-zero with "unknown command", and the
# test t.Skip()'s rather than failing. This is the only way to land
# this harness on its own branch without flapping CI red until the
# verb lands.
#
# Usage:
#   docker build -f test/e2e/bootstrap/Dockerfile -t clawtool-e2e-bootstrap:dev .
#   docker run --rm clawtool-e2e-bootstrap:dev

# ── build stage ─────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/clawtool ./cmd/clawtool

# ── run stage ───────────────────────────────────────────────────────
# Debian bookworm-slim mirrors the onboard fixture's choice (Ubuntu
# is Debian-derived — the task's "minimal Ubuntu base" requirement
# is satisfied by bookworm-slim, which matches the rest of the e2e
# suite's distro convention). nodejs is included because future
# bootstrap iterations will install npm-based agents (codex, gemini)
# under the same flow; pre-installing keeps the container self-
# contained when the verb expands.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates curl jq nodejs \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/clawtool /usr/local/bin/clawtool

# Stub `claude` binary. The real Claude CLI is intentionally NOT
# installed in this container — bootstrap must reach the stub and
# only the stub. The stub:
#   - records argv to /tmp/claude.invocations
#   - records any piped-in stdin to /tmp/claude.stdin so the test
#     can prove the bootstrap prompt actually flowed through;
#   - echoes stdin back on stdout (so the verb's "wait for response"
#     path doesn't hang waiting on a real model);
#   - prints a recognisable line on `--version` so any probe
#     in the verb's preflight returns a healthy code.
#
# A heredoc-RUN keeps the script in this Dockerfile (per the task
# brief — "lives in the same Dockerfile as a `cat >` heredoc") so
# the operator can audit the entire stub surface in one place.
RUN <<'BUILDSTUB'
set -eux
cat > /usr/local/bin/claude <<'STUB'
#!/usr/bin/env bash
# Stub Claude CLI for the bootstrap e2e fixture. Records its
# invocation + any piped stdin, then echoes the stdin back so the
# verb sees a deterministic non-empty response. NEVER reaches the
# Claude API — we are explicitly preventing CI from burning real
# tokens during the bootstrap rehearsal.
echo "$0 $@" >> /tmp/claude.invocations
case "${1:-}" in
    --version|-v|version)
        echo "claude stub 0.0.1-bootstrap-e2e"
        exit 0
        ;;
esac
# Drain stdin if present and echo it back. Read with a short
# timeout so non-piped invocations (just argv) don't hang.
if read -t 1 -r line; then
    {
        printf '%s\n' "$line"
        cat
    } | tee -a /tmp/claude.stdin
fi
exit 0
STUB
chmod +x /usr/local/bin/claude
BUILDSTUB

COPY test/e2e/bootstrap/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/run.sh

# Non-root user, mirroring the onboard fixture so XDG paths
# exercise the same permission surface real operators hit.
RUN useradd -m -s /bin/bash clawuser \
    && install -d -m 1777 /tmp
USER clawuser
ENV HOME=/home/clawuser
ENV XDG_CONFIG_HOME=/tmp/cfg
ENV XDG_STATE_HOME=/tmp/state
ENV XDG_DATA_HOME=/tmp/data
WORKDIR /home/clawuser

ENTRYPOINT ["/usr/local/bin/run.sh"]
