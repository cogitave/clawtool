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
RUN set -eux; \
    for bin in claude codex gemini; do \
        cat > "/usr/local/bin/${bin}" <<EOF ; \
#!/usr/bin/env bash
echo "\$0 \$@" >> "/tmp/${bin}.invocations"
case "\$1" in
    --version|-v|version) echo "${bin} mock 0.0.1-e2e" ;;
    *) ;;
esac
exit 0
EOF
        chmod +x "/usr/local/bin/${bin}" ; \
    done

COPY test/e2e/onboard/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/run.sh

# Run as a non-root operator so the XDG paths exercise the same
# permission surface real users hit.
RUN useradd -m -s /bin/bash operator
USER operator
ENV HOME=/home/operator
WORKDIR /home/operator

ENTRYPOINT ["/usr/local/bin/run.sh"]
