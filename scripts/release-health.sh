#!/usr/bin/env bash
# release-health.sh — quick pre-tag sanity check on recent releases + workflow runs.
#
# Surfaces the last 5 GitHub Releases (asset count) and the last 5 release.yml
# runs (status / conclusion). Flags any release with fewer than 5 assets
# (checksums + 4 binaries) or any run that is failing / in-progress.
#
# Usage:
#   scripts/release-health.sh             # report only, exit 0
#   scripts/release-health.sh --strict    # exit 1 if any unhealthy state
#   scripts/release-health.sh --help      # usage and exit 0
set -euo pipefail

STRICT=0
for arg in "$@"; do
  case "$arg" in
    --strict) STRICT=1 ;;
    --help|-h)
      cat <<EOF
release-health.sh — pre-tag release sanity check.

  --strict   exit 1 if any release has < 5 assets or any recent run failed
  --help     show this message
EOF
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found on PATH" >&2
  exit 2
fi

UNHEALTHY=0
EXPECTED_ASSETS=5

echo "== Last 5 releases =="
mapfile -t TAGS < <(gh release list --limit 5 --json tagName --jq '.[].tagName')
for tag in "${TAGS[@]}"; do
  count=$(gh release view "$tag" --json assets --jq '.assets | length' 2>/dev/null || echo 0)
  if [ "$count" -ge "$EXPECTED_ASSETS" ]; then
    printf '  %s: %d assets ok\n' "$tag" "$count"
  else
    printf '  %s: %d assets MISSING (expected >=%d)\n' "$tag" "$count" "$EXPECTED_ASSETS"
    UNHEALTHY=$((UNHEALTHY + 1))
  fi
done

echo "== Last 5 release.yml runs =="
gh run list --workflow=release.yml --limit 5 \
  --json status,conclusion,headBranch,displayTitle \
  --jq '.[] | "  \(.displayTitle): \(.status) \(.conclusion // "-")"'

bad_runs=$(gh run list --workflow=release.yml --limit 5 \
  --json status,conclusion \
  --jq '[.[] | select(.conclusion == "failure" or .status == "in_progress")] | length')
UNHEALTHY=$((UNHEALTHY + bad_runs))

if [ "$UNHEALTHY" -gt 0 ]; then
  echo "WARN: ${UNHEALTHY} unhealthy release(s)"
  if [ "$STRICT" -eq 1 ]; then
    exit 1
  fi
else
  echo "OK: all recent releases + runs look healthy"
fi
