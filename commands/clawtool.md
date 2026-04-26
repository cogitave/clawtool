---
description: Show clawtool status — version, enabled tools, configured sources, secrets readiness.
allowed-tools: Bash
---

Run the full status check for the user's clawtool installation. Use the
`Bash` tool to invoke the local `clawtool` binary (it's on PATH after
plugin install).

```bash
echo "=== clawtool status ==="
clawtool version
echo ""
echo "=== tools ==="
clawtool tools list
echo ""
echo "=== sources ==="
clawtool source list
echo ""
echo "=== secrets readiness ==="
clawtool source check
```

Then summarize for the user in 3–5 bullets:

- Version and where the binary lives.
- How many tools are enabled vs disabled.
- How many sources are configured and their auth status (✓ ready / ✗ missing).
- One actionable next step if anything is incomplete (e.g. "set the
  GITHUB_TOKEN for github via `clawtool source set-secret github
  GITHUB_TOKEN --value …`").
