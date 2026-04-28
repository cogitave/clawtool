---
description: List every clawtool core tool with its enabled state and the rule that resolved it.
allowed-tools: Bash
---

Wraps `clawtool tools list`. Print the full table verbatim then add a
one-line tip if any core tool is disabled (the user might want
`clawtool tools enable <Name>` to turn it back on).

```bash
clawtool tools list
```

If the user says they want to enable or disable a tool, follow up with
`clawtool tools enable <selector>` or `clawtool tools disable
<selector>`. Selectors are PascalCase for core tools (`Bash`, `Read`,
`Edit`, …) and `<instance>.<tool>` for sourced tools
(`github-personal.create_issue`).
