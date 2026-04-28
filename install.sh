#!/usr/bin/env sh
# install.sh — install clawtool to ~/.local/bin (or $CLAWTOOL_INSTALL_DIR).
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh
#   curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh -s -- --version=v0.8.7
#
# Env overrides (mirror the flag args):
#   CLAWTOOL_VERSION       — pin a specific tag (default: latest GitHub release)
#   CLAWTOOL_INSTALL_DIR   — install destination (default: $HOME/.local/bin)
#
# Behaviour:
#   • Detects OS (linux | darwin) and arch (amd64 | arm64).
#   • Downloads the matching tarball from GitHub Releases.
#   • Verifies SHA-256 against the release's checksums.txt.
#   • Atomic install (temp+rename) so a running clawtool isn't trashed mid-upgrade.
#
# Exits non-zero on any error. Safe to re-run (idempotent upgrades).

set -eu

REPO="cogitave/clawtool"
INSTALL_DIR="${CLAWTOOL_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${CLAWTOOL_VERSION:-latest}"
NO_MODIFY_PATH_HINT=${CLAWTOOL_NO_PATH_HINT:-0}

# ── helpers ──────────────────────────────────────────────────────────

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  GREEN=$(printf '\033[32m')
  RED=$(printf '\033[31m')
  YELLOW=$(printf '\033[33m')
  BOLD=$(printf '\033[1m')
  RESET=$(printf '\033[0m')
else
  GREEN=""; RED=""; YELLOW=""; BOLD=""; RESET=""
fi

err()  { printf '%s✘%s %s\n' "$RED"    "$RESET" "$*" >&2; exit 1; }
warn() { printf '%s!%s %s\n' "$YELLOW" "$RESET" "$*"; }
info() { printf '%s→%s %s\n' "$BOLD"   "$RESET" "$*"; }
ok()   { printf '%s✓%s %s\n' "$GREEN"  "$RESET" "$*"; }

usage() {
  cat <<'EOF'
clawtool installer

Usage:
  install.sh [flags]

Flags:
  --version=<tag>          Pin a release (default: latest, e.g. v0.8.7).
  --install-dir=<path>     Install destination (default: $HOME/.local/bin).
  --no-path-hint           Don't print the PATH instructions.
  -h, --help               This help.

Equivalent env vars:
  CLAWTOOL_VERSION, CLAWTOOL_INSTALL_DIR, CLAWTOOL_NO_PATH_HINT=1
EOF
}

# Pure-shell flag parsing — works under sh/dash/bash.
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version=*)      VERSION=${1#--version=}        ;;
    --version)        VERSION=$2; shift              ;;
    --install-dir=*)  INSTALL_DIR=${1#--install-dir=} ;;
    --install-dir)    INSTALL_DIR=$2; shift          ;;
    --no-path-hint)   NO_MODIFY_PATH_HINT=1          ;;
    -h|--help)        usage; exit 0                  ;;
    *)                err "unknown flag: $1 (--help for usage)" ;;
  esac
  shift
done

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"
}

require_cmd uname
require_cmd tar
require_cmd mkdir
require_cmd grep
require_cmd awk

# Pick a downloader.
if command -v curl >/dev/null 2>&1; then
  DL_CMD=curl
elif command -v wget >/dev/null 2>&1; then
  DL_CMD=wget
else
  err "need curl or wget on PATH"
fi

download() {
  url=$1; out=$2
  case "$DL_CMD" in
    curl) curl -fsSL -o "$out" "$url" || return 1 ;;
    wget) wget -q -O "$out" "$url" || return 1 ;;
  esac
}

download_stdout() {
  url=$1
  case "$DL_CMD" in
    curl) curl -fsSL "$url" || return 1 ;;
    wget) wget -qO- "$url" || return 1 ;;
  esac
}

# Pick a SHA-256 verifier.
if command -v sha256sum >/dev/null 2>&1; then
  SHA_CMD="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  SHA_CMD="shasum -a 256"
else
  err "need sha256sum or shasum to verify checksums"
fi

# ── platform detection ──────────────────────────────────────────────

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux|darwin) ;;
  *) err "unsupported OS: $OS (clawtool ships linux + darwin)" ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) err "unsupported arch: $ARCH" ;;
esac

# ── version resolution ──────────────────────────────────────────────

if [ "$VERSION" = "latest" ]; then
  info "resolving latest release"
  body=$(download_stdout "https://api.github.com/repos/${REPO}/releases/latest") || \
    err "could not fetch release metadata (network down? rate-limited?)"
  VERSION=$(printf '%s' "$body" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$VERSION" ] || err "could not parse latest tag from release metadata"
fi
VERSION_NUM=${VERSION#v}

# ── download + verify + extract ─────────────────────────────────────

ARCHIVE="clawtool_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
URL_TARBALL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
URL_SUMS="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

info "downloading ${ARCHIVE}"
download "$URL_TARBALL" "$TMP/$ARCHIVE" \
  || err "download failed: $URL_TARBALL (does this version + platform exist?)"

info "verifying SHA-256"
download "$URL_SUMS" "$TMP/checksums.txt" \
  || err "could not download checksums.txt for $VERSION"

EXPECTED=$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')
[ -n "$EXPECTED" ] || err "checksums.txt did not list $ARCHIVE"

ACTUAL=$($SHA_CMD "$TMP/$ARCHIVE" | awk '{print $1}')
[ "$EXPECTED" = "$ACTUAL" ] || err "checksum mismatch (expected $EXPECTED, got $ACTUAL)"
ok "checksum verified ($EXPECTED)"

info "extracting"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
[ -x "$TMP/clawtool" ] || err "tarball did not contain a 'clawtool' binary"

# ── install (atomic) ────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR"
TARGET="$INSTALL_DIR/clawtool"
cp "$TMP/clawtool" "$TARGET.new"
chmod +x "$TARGET.new"
mv "$TARGET.new" "$TARGET"
ok "installed clawtool $VERSION to $TARGET"

# Mark this host as installed via the script so the install-event
# telemetry attributes correctly. The marker is read by Go runtime
# via $CLAWTOOL_INSTALL_METHOD; we write it to a tiny env file the
# daemon can read regardless of which shell rc the user runs.
mkdir -p "$HOME/.config/clawtool"
cat > "$HOME/.config/clawtool/install-method" <<METHOD
script
METHOD

# ── PATH hint ───────────────────────────────────────────────────────

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    if [ "$NO_MODIFY_PATH_HINT" != "1" ]; then
      echo
      warn "$INSTALL_DIR is not on \$PATH yet. Add this to your shell rc:"
      printf '\n    export PATH="%s:$PATH"\n\n' "$INSTALL_DIR"
    fi
    ;;
esac

# ── next steps ──────────────────────────────────────────────────────

cat <<EOF

${BOLD}Next steps${RESET}

  ${TARGET} version
  ${TARGET} init                                     # create ~/.config/clawtool/config.toml

  # Register clawtool with Claude Code (zero-friction via the plugin):
  claude plugin marketplace add cogitave/clawtool
  claude plugin install clawtool@clawtool-marketplace

  # Or manually:
  claude mcp add-json clawtool '{"type":"stdio","command":"${TARGET}","args":["serve"]}' --scope user

  # Hard replacement (Claude only sees mcp__clawtool__* equivalents):
  ${TARGET} agents claim claude-code

EOF
