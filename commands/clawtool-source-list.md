---
description: List configured clawtool source instances with their auth-readiness state.
allowed-tools: Bash
---

Wraps `clawtool source list`. Prints the table verbatim. If any source
shows `✗ missing` auth, follow up with the exact `clawtool source
set-secret` command the user needs to run for each missing key.

```bash
clawtool source list
```
