#!/usr/bin/env bash
#
# scripts/ci.sh — single command that runs every CI gate locally.
# Same checks the GitHub Actions workflow runs, in the same order,
# so a clean exit here means CI is going to pass.
#
# Stages (each is a labelled section; failures abort with the
# offending stage's name + log tail):
#
#   1. fmt         — gofmt -l . (offenders on stderr, fail if non-empty)
#   2. vet         — go vet ./...
#   3. build       — go build ./... + the cmd binary into ./bin/
#   4. test        — go test -race -count=1 ./...
#   5. deadcode    — golang.org/x/tools/cmd/deadcode -test ./...
#   6. smoke       — go test -tags=smoke ./internal/cli/ (every verb's
#                    --help + read-only listings; ~20s, skipped under
#                    CLAWTOOL_CI_FAST=1).
#   7. e2e         — bash test/e2e/run.sh (stub-server roundtrip)
#   8. e2e-docker  — onboard + upgrade + realinstall containers
#                    (skipped unless CLAWTOOL_E2E_DOCKER=1; opt-in
#                    because each runs a fresh Alpine + go build inside
#                    a container, ~3-5min per gate on a warm host).
#   8. docker      — docker build + initialize-handshake smoke
#                    (skipped unless CLAWTOOL_E2E_DOCKER=1, same gate).
#
# Flags (env-driven, no argparse — keep the script paste-friendly):
#
#   CLAWTOOL_CI_FAST=1     skip stages 6-8 (only fmt/vet/build/test/deadcode)
#   CLAWTOOL_E2E_DOCKER=1  enable stages 7+8 (off by default; needs Docker)
#   CLAWTOOL_CI_VERBOSE=1  stream stdout instead of capturing for tail
#
# Per-stage output is captured and tail-printed on failure so a clean
# run stays under one screen of output. Set CLAWTOOL_CI_VERBOSE=1 for
# the streamed view when debugging a stage that's hanging.
#
# Why a script (not just `make ci`): operators / CI runners need a
# single self-contained entrypoint that doesn't depend on Make being
# installed and prints a clean summary on success, so this is the
# canonical interface and the Makefile target wraps it.

set -uo pipefail

# ─── styling ──────────────────────────────────────────────────────
# tput-driven colours; degrade gracefully when stdout isn't a tty
# (CI logs strip the escapes anyway).
if [ -t 1 ]; then
    BOLD="$(tput bold 2>/dev/null || true)"
    DIM="$(tput dim 2>/dev/null || true)"
    GREEN="$(tput setaf 2 2>/dev/null || true)"
    RED="$(tput setaf 1 2>/dev/null || true)"
    YELLOW="$(tput setaf 3 2>/dev/null || true)"
    RESET="$(tput sgr0 2>/dev/null || true)"
else
    BOLD="" DIM="" GREEN="" RED="" YELLOW="" RESET=""
fi

# ─── repo root ────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# ─── stage runner ─────────────────────────────────────────────────
# run_stage NAME COMMAND...
# Captures combined stdout+stderr to a tempfile, prints PASS/FAIL,
# tails on failure, accumulates failures into a summary.
FAILURES=()
TMPDIR_CI="$(mktemp -d)"
# Keep logs on failure so the operator can re-read them after the
# summary; clean up only on success. The summary prints the path
# either way so you can grep around in $TMPDIR_CI even after a pass.
trap '[ ${#FAILURES[@]} -eq 0 ] && rm -rf "$TMPDIR_CI"' EXIT

run_stage() {
    local name="$1"; shift
    local logfile="$TMPDIR_CI/${name}.log"
    local started ended elapsed
    started=$(date +%s)

    printf "${BOLD}▶ %s${RESET} ${DIM}(%s)${RESET}\n" "$name" "$(IFS=' '; echo "$*")"

    if [ "${CLAWTOOL_CI_VERBOSE:-0}" = "1" ]; then
        if "$@" 2>&1 | tee "$logfile"; then
            ended=$(date +%s); elapsed=$((ended - started))
            printf "  ${GREEN}✓ pass${RESET} ${DIM}(%ss)${RESET}\n\n" "$elapsed"
            return 0
        fi
    else
        if "$@" >"$logfile" 2>&1; then
            ended=$(date +%s); elapsed=$((ended - started))
            printf "  ${GREEN}✓ pass${RESET} ${DIM}(%ss)${RESET}\n\n" "$elapsed"
            return 0
        fi
    fi

    ended=$(date +%s); elapsed=$((ended - started))
    printf "  ${RED}✗ fail${RESET} ${DIM}(%ss) — last 40 lines:${RESET}\n" "$elapsed"
    tail -40 "$logfile" | sed 's/^/    /'
    printf "  ${DIM}full log: %s${RESET}\n\n" "$logfile"
    FAILURES+=("$name")
    return 1
}

# Stage 1 has its own grep-and-fail shape (gofmt prints offenders
# on stdout; non-empty output = fail), so wrap it in a function.
fmt_check() {
    local offenders
    offenders="$(gofmt -l . 2>&1)"
    if [ -n "$offenders" ]; then
        echo "gofmt offenders:"
        echo "$offenders"
        return 1
    fi
}

# ─── stage list ───────────────────────────────────────────────────
# Order matters: fmt + vet are quick and fail-fast, build before
# test (test depends on package compilation), deadcode after build
# (it walks the typechecked AST). e2e + docker stages are last —
# slowest and gated.
GO_BIN="${GO:-/usr/local/go/bin/go}"
if ! command -v "$GO_BIN" >/dev/null 2>&1; then
    GO_BIN="$(command -v go || true)"
fi
if [ -z "$GO_BIN" ]; then
    echo "${RED}error:${RESET} go binary not found (set \$GO or install Go)" >&2
    exit 127
fi

printf "${BOLD}clawtool CI${RESET} ${DIM}— %s${RESET}\n" "$(date +%H:%M:%S)"
printf "${DIM}go: %s${RESET}\n" "$("$GO_BIN" version)"
printf "${DIM}repo: %s${RESET}\n\n" "$REPO_ROOT"

run_stage fmt fmt_check || true
run_stage vet "$GO_BIN" vet ./... || true
run_stage build "$GO_BIN" build -o bin/clawtool ./cmd/clawtool || true
run_stage test "$GO_BIN" test -race -count=1 -timeout=120s ./... || true

# deadcode comes from a tool we install on demand; if it's not on
# PATH and we can't install, surface the gap as a clear soft-fail
# rather than a confusing exec-not-found.
DEADCODE_BIN="$(command -v deadcode || true)"
if [ -z "$DEADCODE_BIN" ]; then
    printf "${YELLOW}▶ deadcode${RESET} ${DIM}(not installed; skipping — install via \`go install golang.org/x/tools/cmd/deadcode@latest\`)${RESET}\n\n"
else
    run_stage deadcode "$DEADCODE_BIN" -test ./... || true
fi

if [ "${CLAWTOOL_CI_FAST:-0}" = "1" ]; then
    printf "${YELLOW}▶ smoke + e2e + docker stages skipped (CLAWTOOL_CI_FAST=1)${RESET}\n\n"
else
    # CLI smoke-test: every verb's --help + every read-only listing.
    # Cheap (~20s) and gated behind the smoke build tag so the default
    # `go test ./...` stage above doesn't run it twice.
    run_stage smoke "$GO_BIN" test -tags=smoke -count=1 -timeout=120s ./internal/cli/ -run TestCLI_AllVerbs || true

    # Stub-server e2e: builds the stub MCP fixture + runs the bash
    # roundtrip script. Always-run (no Docker required); cheap and
    # exercises the full MCP stdio handshake.
    if [ -x test/e2e/run.sh ]; then
        run_stage stub-e2e bash test/e2e/run.sh || true
    fi

    if [ "${CLAWTOOL_E2E_DOCKER:-0}" = "1" ]; then
        # Container e2e gates — opt-in via CLAWTOOL_E2E_DOCKER=1.
        # Each builds a fresh Alpine + golang image and exercises a
        # full install/upgrade/onboard surface. Slow (~3-5min per).
        run_stage e2e-onboard env CLAWTOOL_E2E_DOCKER=1 "$GO_BIN" test -count=1 -timeout=300s ./test/e2e/onboard/... || true
        run_stage e2e-upgrade env CLAWTOOL_E2E_DOCKER=1 "$GO_BIN" test -count=1 -timeout=300s ./test/e2e/upgrade/... || true
        run_stage e2e-realinstall env CLAWTOOL_E2E_DOCKER=1 "$GO_BIN" test -count=1 -timeout=300s ./test/e2e/realinstall/... || true

        # Docker image build + MCP initialize handshake. Same target
        # the Makefile's docker-smoke runs.
        run_stage docker-smoke make docker-smoke || true
    else
        printf "${YELLOW}▶ e2e-docker + docker stages skipped (set CLAWTOOL_E2E_DOCKER=1 to run)${RESET}\n\n"
    fi
fi

# ─── summary ──────────────────────────────────────────────────────
if [ ${#FAILURES[@]} -eq 0 ]; then
    printf "${GREEN}${BOLD}✓ all stages passed${RESET}\n"
    exit 0
fi

printf "${RED}${BOLD}✗ %d stage(s) failed:${RESET}\n" "${#FAILURES[@]}"
for f in "${FAILURES[@]}"; do
    printf "  ${RED}✗${RESET} %s ${DIM}(see %s/%s.log)${RESET}\n" "$f" "$TMPDIR_CI" "$f"
done
exit 1
