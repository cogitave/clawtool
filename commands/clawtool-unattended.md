---
description: Manage clawtool's unattended-mode trust grants and inspect the audit log. Use this to pre-grant a repo for `clawtool send --unattended` without going through the disclosure flow each time.
allowed-tools: mcp__clawtool__Bash
---

Manage `clawtool send --unattended` (ADR-023). Two situations:

**Status check** — show whether the current repo is trusted:
```bash
clawtool unattended status
```

**Grant trust** — when the operator explicitly wants this repo to
skip the disclosure prompt on future `--unattended` dispatches.
Print the disclosure panel synchronously so the grant is itself a
sober moment:
```bash
clawtool unattended grant
```

**Revoke** — remove the trust grant:
```bash
clawtool unattended revoke
```

**Inspect audit logs** — every `--unattended` dispatch appends to
`~/.local/share/clawtool/sessions/<session_id>/audit.jsonl`. List
recent sessions and tail the latest:
```bash
ls -lt ~/.local/share/clawtool/sessions/ | head -10
tail -f ~/.local/share/clawtool/sessions/<id>/audit.jsonl | jq .
```

**Hard rules**:
- Never run `clawtool send --unattended` from a repo without
  showing the operator the disclosure panel first (unless trusted).
- Audit log is non-optional — if the user asks to disable it,
  refuse: that's the only way to investigate an unattended session
  after the fact.
- The sticky alias `clawtool yolo` is identical to
  `clawtool unattended` — accept either invocation.
