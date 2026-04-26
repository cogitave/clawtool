---
description: Discover clawtool tools by natural-language query (BM25 ranking via mcp__clawtool__ToolSearch).
allowed-tools: mcp__clawtool__ToolSearch
argument-hint: <natural-language query>
---

Use `mcp__clawtool__ToolSearch` to rank candidates for the user's query.
Default to limit=8. Pass through the user's argument verbatim as the
`query`.

```
mcp__clawtool__ToolSearch{query: "$ARGUMENTS", limit: 8}
```

After the call returns, show the top 3-5 hits in the form:

```
1. <name> (score 0.92) — <description excerpt>   [core | sourced<instance>]
2. ...
```

Then, if the top hit is unambiguous (score > 0.5 and ahead of #2 by a
clear margin), suggest the next call the user could make with that
tool. If results are weak (top score < 0.2), recommend the user either
add a source via `/clawtool-source-add` or clarify what they want to
do.
