---
type: decision
title: "003 Multi-account git via direnv and gh"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - infra
  - git
status: mature
related:
  - "[[002 Vault on Windows filesystem]]"
---

# 003 — Multi-account git via direnv and gh

## Context

User has two GitHub identities:
- **Personal** (`bahadirarda`, `bahadirarda96@gmail.com`) — used for clawtool and `~/workspaces/@cogitave/`.
- **Work** (`caucasian01`, `claude@caucasian.com.tr`) — used for separate work projects.

User runs multiple Claude Code / terminal sessions in parallel across both contexts. `gh auth switch` was rejected outright — global state per terminal does not survive parallel work.

SSH config already has `Host gh-bahadirarda` and `Host gh-caucasian01` aliases pointing at distinct identity files. Plain `Host github.com` was unconfigured — that's why `claude plugin install` defaulted to SSH and failed first time.

## Decision

Three-layer per-directory isolation:

| Layer | Mechanism | Effect |
|---|---|---|
| **gh CLI auth** | `GH_CONFIG_DIR=~/.config/gh-{personal,work}` set per project via direnv | Each terminal has its own gh state |
| **Git identity (name/email)** | Global `~/.gitconfig` with `[includeIf "gitdir:..."]` → split files (`~/.gitconfig.personal`, `~/.gitconfig.work`) | Identity resolves automatically by repo location |
| **SSH key selection** | `core.sshCommand` inside the includeIf'd file | Right key picked without env vars |

`.envrc` in clawtool:
```bash
export GH_CONFIG_DIR="$HOME/.config/gh-personal"
```

`~/.gitconfig`:
```gitconfig
[includeIf "gitdir:~/workspaces/@cogitave/"]
    path = ~/.gitconfig.personal
[includeIf "gitdir:/mnt/c/Users/Arda/workspaces/@cogitave/"]
    path = ~/.gitconfig.personal
```

## Rationale

- **No mode switching.** Open as many parallel terminals as needed; each one's identity is determined by where it's `cd`'d to. Zero risk of pushing with wrong author.
- **Env-based for gh, file-based for git.** gh CLI doesn't support `[includeIf]`-style config selection; it reads `GH_CONFIG_DIR` or defaults to `~/.config/gh`. direnv handles the env half. Git natively supports `[includeIf]`, so no env var needed there.
- **No global rewrites, no SSH-config surgery.** Did NOT add a `Host github.com` block (would conflict with multi-account intent), did NOT do a `url.https.insteadOf` rewrite (was rejected as too broad).
- **Plugin install reused the same SSH key once.** When `claude plugin install` failed via SSH, used `GH_SSH_COMMAND="ssh -i ~/.ssh/id_ed25519_gh-caucasian01"` scoped to that single invocation — no permanent state change.

## Alternatives Rejected

- **`git config --global url."https://github.com/".insteadOf "git@github.com:"`** — too broad, persistent global change rewriting all SSH github URLs to HTTPS.
- **`gh auth switch`** — global per-shell state, breaks parallel terminals.
- **Per-repo `git config` only (no global setup)** — works for the one repo but every new project requires re-config; doesn't solve gh CLI tier.
- **direnv + `.env` files committed to repo** — can leak environment-specific paths to teammates.

## Consequences

- Personal auth migrated from `~/.config/gh/` (default, wrong) to `~/.config/gh-personal/` after user ran `gh auth login` without `GH_CONFIG_DIR` set. Default `~/.config/gh/` then deleted.
- Work auth still pending: user runs `GH_CONFIG_DIR=~/.config/gh-work gh auth login` when needed.
- `~/.gitconfig.work` exists with `claude@caucasian.com.tr` already filled; `name` is placeholder until work auth completes and we fetch via `gh api user`.
- `~/.bashrc` now hooks direnv: `eval "$(direnv hook bash)"`.
- Tools installed user-local (no sudo): `~/.local/bin/direnv` (2.37.1), `~/.local/bin/gh` (2.91.0).

## Status

Personal layer fully verified 2026-04-26: `git config user.email` resolves to `bahadirarda96@gmail.com` inside clawtool, gh API confirms `bahadirarda` login.
