---
type: decision
title: "002 Vault on Windows filesystem"
created: 2026-04-26
updated: 2026-04-26
tags:
  - decision
  - adr
  - infra
status: mature
related:
  - "[[001 Choose claude-obsidian as brain layer]]"
---

# 002 — Vault on Windows filesystem

## Context

Initial plan was to keep the clawtool repo (and therefore the vault) in WSL home (`/home/arda/workspaces/@cogitave/clawtool`) and access from Windows Obsidian via UNC path `\\wsl.localhost\Ubuntu-22.04\...`.

Two failures encountered:
1. Pasting the UNC into Obsidian's Open Folder dialog returned `EISDIR: illegal operation on a directory, watch '...'` — Obsidian's file watcher (chokidar / Node `fs.watch`) does not work over WSL's 9P UNC paths.
2. Drive mapping (`net use Z: \\wsl$\Ubuntu-22.04\...`) was a thin alias to the same UNC — same failure.

## Decision

Move clawtool entirely to Windows native filesystem at `C:\Users\Arda\workspaces\@cogitave\clawtool`. Access from WSL via `/mnt/c/Users/Arda/workspaces/@cogitave/clawtool`.

## Rationale

- Obsidian's watcher requires a real OS-native filesystem.
- WSL's `/mnt/c` mount is bidirectional and live — same physical files, two paths.
- WSL operations on `/mnt/c` are mildly slower than `~/` (ext4) but acceptable for our workload (text editing, git, hooks, `gh`).
- File ownership remains correct from both sides. Permission bits show as `rwxrwxrwx` from WSL because NTFS doesn't carry unix bits — cosmetic only, not a real access issue.

## Alternatives Rejected

- **Symlink WSL home → /mnt/c** — works but adds a moving part; better to commit to a single canonical path.
- **Run Obsidian inside WSL via WSLg** — heavier, requires X server config; not worth it for one app.
- **Obsidian on a different host accessing WSL via SSHFS / SMB** — overkill.

## Consequences

- Canonical path going forward: `/mnt/c/Users/Arda/workspaces/@cogitave/clawtool`.
- `~/.gitconfig` carries two `[includeIf]` patterns to match both old and new locations (defensive — old WSL path no longer holds clawtool but the rule is harmless).
- The empty `/home/arda/workspaces/@cogitave/clawtool/` directory remains because the Bash harness pinned its cwd there at one point; it contains only a `.placeholder` file. Safe to delete later.
- Z: drive map (`net use Z:`) from earlier troubleshooting is broken (it pointed at the dead WSL UNC). Should be removed: `net use Z: /delete`.

## Status

Implemented 2026-04-26. Migration done via `cp -a` (cross-filesystem); `.git/`, `.envrc`, `.obsidian/` preserved with permissions adjusted to NTFS semantics.
