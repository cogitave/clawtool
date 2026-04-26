---
type: entity
title: "Bahadır Arda"
created: 2026-04-26
updated: 2026-04-26
tags:
  - entity
  - person
  - owner
status: evergreen
entity_type: person
role: "clawtool owner / primary developer"
related:
  - "[[003 Multi-account git via direnv and gh]]"
---

# Bahadır Arda

Project owner of clawtool.

## Identity Setup

| Context | Identity |
|---|---|
| **Personal** (clawtool, ~/workspaces/@cogitave/) | `bahadirarda` · `bahadirarda96@gmail.com` · SSH key `id_ed25519_gh-bahadirarda` · gh config `~/.config/gh-personal/` |
| **Work** (separate project paths, TBD) | `caucasian01` · `claude@caucasian.com.tr` · SSH key `id_ed25519_gh-caucasian01` · gh config `~/.config/gh-work/` |

Per-directory isolation via direnv + git `[includeIf]`. Details in [[003 Multi-account git via direnv and gh]].

## Working Style Preferences

- Communicates in **Turkish** for conversational text; technical identifiers stay in original form.
- Wants **research and specs before implementation** for clawtool — foundation must be right.
- Rejects work-style tools that require global mode-switching (e.g. `gh auth switch`); prefers per-context isolation.
- Distinguishes work-style guidance ("how Claude should work") from project features. Don't mix the two.
