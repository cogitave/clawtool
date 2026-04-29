# test/e2e/realinstall/Dockerfile — REAL install.sh + REAL release
# tarball + REAL onboard flow on a clean Alpine 3.20 box. The other
# e2e fixtures (test/e2e/upgrade, test/e2e/onboard) build clawtool
# from source via `go build -ldflags`; that proves the post-install
# code path but never exercises the actual GitHub-release download
# users hit on day one.
#
# This container deliberately uses Alpine because:
#   1. it's the smallest realistic distro most operators reach for
#      ("docker run -it alpine sh" is the canonical "fresh box");
#   2. it uses musl libc — the release binary has to be statically
#      linked (goreleaser's CGO_ENABLED=0 default) for `clawtool
#      --version` to even start. If we're inadvertently shipping a
#      glibc-linked binary, this test catches it the second the
#      container runs.
#
# What the container does (driven by run.sh):
#   1. Curls install.sh COPY'd in from the repo (byte-identical to
#      the script users get via `curl -sSL …/install.sh | sh`).
#   2. install.sh detects linux/amd64, fetches the real release
#      tarball + checksums.txt from cogitave/clawtool's GitHub
#      releases, verifies sha256, atomic-installs to ~/.local/bin.
#   3. clawtool --version, daemon start, daemon status, /v1/health.
#   4. clawtool onboard --yes against mock host CLIs (claude / codex
#      / gemini), so the wizard's full state machine fires.
#   5. clawtool upgrade --check (network round-trip to GitHub for
#      the release feed; no install).
#   6. daemon stop + clean shutdown verification.
#
# The container is named + labelled so it shows up in Docker
# Desktop after the test exits, and the harness reports the
# `docker rm -f` cleanup line. Cleanup is intentionally manual so
# the operator can poke at /tmp/cfg/clawtool, /tmp/state/clawtool,
# and ~/.local/bin/clawtool by hand if a test fails.
#
# Usage:
#   docker build -f test/e2e/realinstall/Dockerfile -t clawtool-e2e-realinstall:test .
#   docker run --name clawtool-e2e-realinstall clawtool-e2e-realinstall:test

FROM alpine:3.20

# install.sh needs: curl (download) + tar (extract tarball) +
# coreutils (sha256sum — busybox's variant is fine but installing
# coreutils mirrors the toolchain a normal Alpine user gets after
# a few weeks of usage). bash isn't strictly required (the script
# is `#!/usr/bin/env sh` and works in dash/ash) but onboard's mock
# CLIs and the run-harness use bash idioms.
#
# jq is for parsing daemon.json / health responses inside the
# harness — same convention the upgrade fixture uses, keeps the
# two suites symmetric.
#
# ca-certificates is needed because Alpine's default cert store
# has fewer CAs than Debian; install.sh's `curl -sSL` against
# api.github.com fails with `SSL certificate problem` without it.
RUN apk add --no-cache \
        bash \
        ca-certificates \
        coreutils \
        curl \
        jq \
        procps \
        tar

# Mock host CLIs at /usr/local/bin/claude|codex|gemini. The onboard
# wizard probes each via `--version` to pick a primary; without these
# stubs `clawtool onboard --yes` would fall back to the no-host path
# and skip the bridge-install + agent-claim half of the wizard, which
# is exactly the half this fixture wants to exercise.
RUN <<'BUILDMOCK'
set -eux
mkmock() {
    local name="$1"
    cat > "/usr/local/bin/${name}" <<MOCK
#!/usr/bin/env sh
echo "\$0 \$@" >> "/tmp/${name}.invocations"
case "\$1" in
    --version|-v|version) echo "${name} mock 0.0.1-realinstall" ;;
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

# install.sh is COPY'd in from the repo — byte-identical to the
# version users curl from raw.githubusercontent.com. The harness
# runs it via `sh install.sh`, mirroring the documented one-liner
# `curl -sSL …/install.sh | sh`.
COPY install.sh /usr/local/bin/clawtool-install.sh
COPY test/e2e/realinstall/run.sh /usr/local/bin/run.sh
RUN chmod +x /usr/local/bin/clawtool-install.sh /usr/local/bin/run.sh

# XDG roots — same convention as the upgrade fixture so any
# `docker exec ... clawtool …` invocations pick up the daemon's
# state instead of falling back to $HOME/.config.
ENV XDG_CONFIG_HOME=/tmp/cfg
ENV XDG_STATE_HOME=/tmp/state
ENV XDG_DATA_HOME=/tmp/data
# install.sh defaults to $HOME/.local/bin — the real-world path
# every non-root Linux user hits. We deliberately leave it
# unspecified so the test exercises the documented default.
# (Earlier draft pinned to /usr/local/bin which clawuser can't
# write to — same permission error a real user would see if
# they `sudo`'d the wrong way.)

# Run as a non-root user so the install path exercises the
# permission surface real users hit. `clawuser` matches the
# convention the onboard fixture uses (Debian's `operator` is
# reserved; alpine doesn't have that conflict but we keep the
# name consistent across fixtures so a future shared user-creation
# helper has a single canonical name to look for).
RUN adduser -D -s /bin/bash clawuser
# Mock-CLI invocation logs land at /tmp/<name>.invocations; the
# user needs write access there.
RUN install -d -m 1777 /tmp
USER clawuser
ENV HOME=/home/clawuser
WORKDIR /home/clawuser

ENTRYPOINT ["/usr/local/bin/run.sh"]
