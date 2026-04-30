#!/usr/bin/env bash
# Smoke test for release-health.sh — only checks --help works without invoking gh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${SCRIPT_DIR}/release-health.sh"

if [ ! -x "$TARGET" ] && [ ! -r "$TARGET" ]; then
  echo "FAIL: ${TARGET} not found" >&2
  exit 1
fi

out=$(bash "$TARGET" --help)
rc=$?

if [ "$rc" -ne 0 ]; then
  echo "FAIL: --help exited ${rc}" >&2
  exit 1
fi

if [ -z "$out" ]; then
  echo "FAIL: --help produced empty output" >&2
  exit 1
fi

echo "ok: release-health.sh --help"
