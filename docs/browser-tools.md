# clawtool Browser tools

clawtool wraps **[Obscura](https://github.com/h4ckf0r0day/obscura)** —
an Apache-2.0 Rust headless browser engine (V8 + Chrome DevTools
Protocol, single 70 MB static binary, 30 MB memory footprint, drop-in
for Puppeteer / Playwright) — to give agents a way to render JS-heavy
content the way a real browser sees it.

> **`Tool` not `Transport`.** clawtool's `SendMessage` only dispatches
> prompts to upstreams that publish a stable headless contract
> (claude / codex / opencode / gemini). Browser-driven LLM portals
> have no such contract, change weekly, and break Terms of Service.
> The browser tools are general-purpose — they don't know or care
> about DeepSeek / ChatGPT / Claude.ai. The operator wires the URL +
> selectors + cookies; clawtool just runs the browser.

## Install Obscura

```sh
# Linux x86_64
curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-x86_64-linux.tar.gz
tar xzf obscura-x86_64-linux.tar.gz && sudo mv obscura /usr/local/bin/

# macOS Apple Silicon
curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-aarch64-macos.tar.gz
tar xzf obscura-aarch64-macos.tar.gz && sudo mv obscura /usr/local/bin/

# macOS Intel
curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-x86_64-macos.tar.gz
tar xzf obscura-x86_64-macos.tar.gz && sudo mv obscura /usr/local/bin/
```

Verify: `obscura --help`. Each browser tool detects the binary at
startup and surfaces the same install hint when it's missing.

## Tools

### `BrowserFetch` — JS-rendered single-page fetch

Sister to `WebFetch` (server-side via Mozilla Readability). Use this
when WebFetch returns an empty Next.js / React shell.

| Arg | Default | Notes |
| --- | --- | --- |
| `url` | (required) | http:// or https:// |
| `wait_until` | `networkidle0` | `load` / `domcontentloaded` / `networkidle0` |
| `selector` | (none) | CSS selector to wait for before dumping |
| `eval` | (none) | JavaScript expression to evaluate; result lands in `eval_result` |
| `stealth` | `false` | Pass `--stealth` (anti-fingerprinting + tracker blocking) |
| `timeout_ms` | 30000 | Hard deadline; max 180000 |

Result shape mirrors `WebFetch` (title / byline / sitename / content)
plus `eval_result` when `eval` is set, so an agent can swap the two
without rewriting parsing.

### `BrowserScrape` — bulk parallel render

Wraps `obscura scrape <url...> --concurrency N --eval ... --format json`.
Each URL gets its own browser context — no shared state.

| Arg | Default | Notes |
| --- | --- | --- |
| `urls` | (required) | Newline- or comma-separated. Hard cap 500 URLs. |
| `eval` | (required) | Per-page JS expression. |
| `concurrency` | 10 | Parallel workers. Hard cap 50. |
| `wait_until` | `networkidle0` | Same vocabulary as `BrowserFetch`. |
| `stealth` | `false` | |
| `timeout_ms` | 120000 | Whole-batch deadline. Max 600000. |

Output is one row per URL with either `result` or `error` populated.

### `BrowserAction` — cookie-driven interactive flows

> Coming in the v0.16.1 follow-up. Drives Obscura's CDP server
> (`obscura serve --port 9222`) over WebSocket so the operator can
> inject cookies + headers, click / type / wait through a multi-step
> flow, and capture the final state. The interactive surface is a
> separate file because cookie injection requires CDP — the
> `obscura fetch` CLI doesn't accept cookie flags. Tracked in the
> v0.16 roadmap.

## Worked example — fetch a Next.js docs page

```jsonc
// MCP call (from inside Claude Code, Codex, etc.):
{
  "tool": "BrowserFetch",
  "args": {
    "url": "https://nextjs.org/docs/app/api-reference/file-conventions/metadata",
    "wait_until": "networkidle0",
    "selector": "main article"
  }
}
```

Returns `title`, `byline`, `content` (extracted prose). `WebFetch` on
the same URL would return a partial shell because Next.js renders the
real docs body client-side.

## Worked example — bulk scrape blog headlines

```jsonc
{
  "tool": "BrowserScrape",
  "args": {
    "urls": "https://blog.a.test\nhttps://blog.b.test\nhttps://blog.c.test",
    "eval": "document.querySelector('h1')?.textContent || ''",
    "concurrency": 5,
    "wait_until": "networkidle0"
  }
}
```

Each row carries the captured `h1` text or a per-URL error so the
batch keeps going through individual failures.

## Failure modes

| Symptom | Cause | Fix |
| --- | --- | --- |
| `obscura binary not on PATH` | install hint surfaced | follow the curl one-liner above |
| `obscura timed out after Nms` | page never reaches `wait_until` state | bump `timeout_ms`, switch to `domcontentloaded`, or pin a `selector` |
| `obscura: exit status 2` | upstream Obscura crashed | check stderr included in `error_reason`; usually a malformed `eval` expression |
| empty `content` for an SPA | rendered before hydration completed | use `selector` instead of `wait_until=load` |

## Why not Headless Chrome?

| Metric | Obscura | Headless Chrome |
| --- | --- | --- |
| Memory | 30 MB | 200+ MB |
| Binary size | 70 MB | 300+ MB |
| Page load | ~85 ms | ~500 ms |
| Startup | instant | ~2 s |
| Anti-detect | built-in | none |
| Puppeteer / Playwright | yes | yes |

We wrap whichever engine has the right shape; Obscura won the slot
because its CDP API is broad enough for our browser surface and the
binary is small enough to ship next to clawtool's ~50 MB Go binary
without doubling the install cost.

## Cross-references

- `internal/tools/core/browser_fetch.go` and
  `internal/tools/core/browser_scrape.go` — implementations.
- `docs/http-api.md` — Postman / cURL recipes for the HTTP gateway,
  which exposes these MCP tools at `/mcp` when started with
  `--mcp-http`.
