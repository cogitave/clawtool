#!/usr/bin/env bash
# release-notes-rich.sh — self-hosted release-body generator.
#
# Replaces the per-release output of orhun/git-cliff-action (which
# emits a flat "## clawtool vX.Y.Z" header + static install snippet
# with no grouped commits). This script reads conventional-commits
# subjects between two refs and produces a richer markdown body:
#
#   - Header + 1-line summary derived from highest-impact commit.
#   - Features grouped by scope (catalog, agents, cli, recipes,
#     rules, tools, portal, playbooks, setup) with per-scope emoji
#     sub-headers.
#   - Fixes grouped by scope.
#   - Collapsed Docs / Tests / Chore section.
#   - Loud Breaking Changes block (`type!:` or `BREAKING CHANGE:`).
#   - Static install snippet preserved from the previous body.
#   - Stats footer: N commits, M contributors, +X/-Y lines.
#
# Usage:
#   scripts/release-notes-rich.sh --from <tag> --to <tag>     # explicit range
#   scripts/release-notes-rich.sh --to <tag>                  # auto previous tag
#   scripts/release-notes-rich.sh                             # last tag → HEAD
#
# Output: markdown to stdout (consumed by goreleaser --release-notes).

set -euo pipefail

FROM=""
TO=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --from) FROM="$2"; shift 2 ;;
    --to)   TO="$2"; shift 2 ;;
    --help|-h)
      sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Resolve TO: explicit > GITHUB_REF_NAME (in CI) > HEAD.
if [ -z "$TO" ]; then
  TO="${GITHUB_REF_NAME:-HEAD}"
fi

# Resolve FROM: previous tag reachable from TO. `git describe --tags
# --abbrev=0 <to>^` gives the closest tag strictly before TO.
if [ -z "$FROM" ]; then
  FROM="$(git describe --tags --abbrev=0 "${TO}^" 2>/dev/null || true)"
  if [ -z "$FROM" ]; then
    # No previous tag — first release. Use the root commit.
    FROM="$(git rev-list --max-parents=0 HEAD | head -1)"
  fi
fi

REPO_URL="https://github.com/cogitave/clawtool"
RANGE="${FROM}..${TO}"

# ─── scope → emoji map ────────────────────────────────────────────
scope_emoji() {
  case "$1" in
    catalog)   echo "📦" ;;
    agents)    echo "🤖" ;;
    cli)       echo "🖥️" ;;
    recipes)   echo "🍳" ;;
    rules)     echo "🛡️" ;;
    tools)     echo "🛠️" ;;
    portal)    echo "🌉" ;;
    playbooks) echo "📓" ;;
    setup)     echo "⚙️" ;;
    *)         echo "🔹" ;;
  esac
}

# ─── parse commits ────────────────────────────────────────────────
# Use a record separator we'd never see in a subject. Format:
#   <sha>\t<short>\t<subject>\t<body-first-line>
# Body is fetched separately via `git show -s` for BREAKING CHANGE
# detection — keeping the main loop input simple.
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

LOG="$TMPDIR/log.txt"
# tformat (vs format) appends a newline AFTER each record including
# the last one — without it, `while read` silently drops the final
# commit because the line has no terminator.
git log "$RANGE" --no-merges --pretty=tformat:'%H%x09%h%x09%s' >"$LOG" || true

FEAT_SCOPES_FILE="$TMPDIR/feat-scopes"
FIX_SCOPES_FILE="$TMPDIR/fix-scopes"
: >"$FEAT_SCOPES_FILE"
: >"$FIX_SCOPES_FILE"

declare -A FEAT_LINES   # scope → newline-joined lines
declare -A FIX_LINES
DOCS_LINES=""
CHORE_LINES=""
BREAKING_LINES=""
HIGHLIGHT_SUBJECT=""
HIGHLIGHT_TYPE_RANK=99   # lower = more impactful (breaking=0, feat=1, fix=2)

rank_of() {
  case "$1" in
    breaking) echo 0 ;;
    feat)     echo 1 ;;
    fix)      echo 2 ;;
    *)        echo 9 ;;
  esac
}

while IFS=$'\t' read -r sha short subject; do
  [ -z "$sha" ] && continue

  # Skip the autocommitted "regenerate changelog" entries — they're
  # release-bot noise, not changes the operator wants to see again.
  case "$subject" in
    "docs(changelog): regenerate"*) continue ;;
  esac

  # Parse `<type>(<scope>)?(!)?: <subject>`.
  ctype=""
  cscope=""
  cbang=""
  csubj="$subject"
  CC_RE='^([a-z]+)(\(([^)]+)\))?(!)?:[[:space:]](.*)$'
  if [[ "$subject" =~ $CC_RE ]]; then
    ctype="${BASH_REMATCH[1]}"
    cscope="${BASH_REMATCH[3]}"
    cbang="${BASH_REMATCH[4]}"
    csubj="${BASH_REMATCH[5]}"
  fi

  # BREAKING CHANGE: trailer in the body.
  body="$(git show -s --format=%b "$sha" 2>/dev/null || true)"
  has_breaking=0
  if [ -n "$cbang" ]; then has_breaking=1; fi
  if echo "$body" | grep -qE '^BREAKING CHANGE:'; then has_breaking=1; fi

  link="[\`${short}\`](${REPO_URL}/commit/${sha})"
  line="- ${csubj} (${link})"

  if [ "$has_breaking" -eq 1 ]; then
    BREAKING_LINES+="${line}"$'\n'
    if [ "$(rank_of breaking)" -lt "$HIGHLIGHT_TYPE_RANK" ]; then
      HIGHLIGHT_TYPE_RANK="$(rank_of breaking)"
      HIGHLIGHT_SUBJECT="$csubj"
    fi
  fi

  case "$ctype" in
    feat)
      key="${cscope:-_}"
      FEAT_LINES["$key"]+="${line}"$'\n'
      grep -qxF "$key" "$FEAT_SCOPES_FILE" 2>/dev/null || echo "$key" >>"$FEAT_SCOPES_FILE"
      if [ "$(rank_of feat)" -lt "$HIGHLIGHT_TYPE_RANK" ]; then
        HIGHLIGHT_TYPE_RANK="$(rank_of feat)"
        HIGHLIGHT_SUBJECT="$csubj"
      fi
      ;;
    fix)
      key="${cscope:-_}"
      FIX_LINES["$key"]+="${line}"$'\n'
      grep -qxF "$key" "$FIX_SCOPES_FILE" 2>/dev/null || echo "$key" >>"$FIX_SCOPES_FILE"
      if [ "$(rank_of fix)" -lt "$HIGHLIGHT_TYPE_RANK" ]; then
        HIGHLIGHT_TYPE_RANK="$(rank_of fix)"
        HIGHLIGHT_SUBJECT="$csubj"
      fi
      ;;
    docs|test|tests)
      DOCS_LINES+="${line}"$'\n'
      ;;
    chore|build|ci|style|refactor|perf|revert)
      CHORE_LINES+="${line}"$'\n'
      ;;
    *)
      CHORE_LINES+="${line}"$'\n'
      ;;
  esac
done <"$LOG"

# ─── stats ────────────────────────────────────────────────────────
NCOMMITS=$(wc -l <"$LOG" | tr -d ' ')
NCONTRIB=$(git log "$RANGE" --no-merges --format='%an' 2>/dev/null | sort -u | grep -cv '^$' || true)
SHORTSTAT=$(git log "$RANGE" --no-merges --shortstat --format= 2>/dev/null \
  | awk '{ for (i=1;i<=NF;i++){ if ($i ~ /insertion/) ins+=$(i-1); if ($i ~ /deletion/) del+=$(i-1) } } END { printf "+%d/-%d", ins+0, del+0 }')

# ─── render ───────────────────────────────────────────────────────
TAG_DISPLAY="$TO"
[ "$TAG_DISPLAY" = "HEAD" ] && TAG_DISPLAY="$(git rev-parse --short HEAD)"

echo "## clawtool ${TAG_DISPLAY}"
echo
if [ -n "$HIGHLIGHT_SUBJECT" ]; then
  echo "_${HIGHLIGHT_SUBJECT}_"
else
  echo "_Maintenance release._"
fi
echo

# Breaking — surfaced first, loud.
if [ -n "$BREAKING_LINES" ]; then
  echo "### ⚠️ Breaking changes"
  echo
  printf '%s' "$BREAKING_LINES"
  echo
fi

# Features grouped by scope.
if [ -s "$FEAT_SCOPES_FILE" ]; then
  echo "### 🚀 Features"
  echo
  while IFS= read -r scope; do
    [ -z "$scope" ] && continue
    label="$scope"
    [ "$scope" = "_" ] && label="general"
    emoji="$(scope_emoji "$label")"
    echo "#### ${emoji} ${label}"
    echo
    printf '%s' "${FEAT_LINES[$scope]}"
    echo
  done < <(sort "$FEAT_SCOPES_FILE")
fi

# Fixes grouped by scope.
if [ -s "$FIX_SCOPES_FILE" ]; then
  echo "### 🐛 Fixes"
  echo
  while IFS= read -r scope; do
    [ -z "$scope" ] && continue
    label="$scope"
    [ "$scope" = "_" ] && label="general"
    emoji="$(scope_emoji "$label")"
    echo "#### ${emoji} ${label}"
    echo
    printf '%s' "${FIX_LINES[$scope]}"
    echo
  done < <(sort "$FIX_SCOPES_FILE")
fi

# Docs / tests / chore — collapsed.
if [ -n "$DOCS_LINES" ] || [ -n "$CHORE_LINES" ]; then
  echo "<details>"
  echo "<summary>📚 Docs, tests, chore</summary>"
  echo
  if [ -n "$DOCS_LINES" ]; then
    printf '%s' "$DOCS_LINES"
  fi
  if [ -n "$CHORE_LINES" ]; then
    printf '%s' "$CHORE_LINES"
  fi
  echo
  echo "</details>"
  echo
fi

# Install snippet — preserved verbatim from the prior static block.
INSTALL_VER="${TAG_DISPLAY#v}"
cat <<EOF
---

**Install (user-local, no sudo)**

\`\`\`bash
curl -sSL ${REPO_URL}/releases/download/${TAG_DISPLAY}/clawtool_${INSTALL_VER}_linux_amd64.tar.gz \\
  | tar -xz -C ~/.local/bin clawtool
clawtool init
claude mcp add-json clawtool '{"type":"stdio","command":"'"\$HOME"'/.local/bin/clawtool","args":["serve"]}' --scope user
\`\`\`

EOF

# Stats footer.
echo "---"
echo
echo "**Stats:** ${NCOMMITS} commits · ${NCONTRIB} contributor(s) · ${SHORTSTAT} lines · [\`${FROM}...${TAG_DISPLAY}\`](${REPO_URL}/compare/${FROM}...${TAG_DISPLAY})"
