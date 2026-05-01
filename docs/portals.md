# clawtool Portals

A **portal** is a saved web-UI target — a base URL paired with login
cookies, CSS selectors, and a "response done" predicate — that
clawtool can drive on your behalf so an MCP-aware agent can ask it
questions like any other agent.

> Portals are a **Tool surface, not a Transport**. The
> supervisor still only dispatches to upstreams that publish a stable
> headless contract (claude / codex / opencode / gemini). Portals
> live next to BrowserFetch / BrowserScrape and are explicitly
> per-operator: ToS / DOM-drift / cookie expiry are your concerns,
> not clawtool's.

## When to use a portal vs. an agent

| You want… | Use |
| --- | --- |
| Codex / Claude / Gemini / OpenCode via their CLI | `clawtool send` (agents) |
| A free / no-API LLM web UI you have a login for | `clawtool portal ask` |
| Static HTML page (no JS) | `WebFetch` |
| SPA / Next.js / hydrated page | `BrowserFetch` |
| 50 SPA pages in parallel | `BrowserScrape` |
| One-off interactive flow against a known site | (planned: `BrowserAction`) |

## Surface (v0.16.1)

```
clawtool portal list                  # configured portals + auth-cookie names
clawtool portal which                 # sticky default
clawtool portal use <name>            # set sticky default
clawtool portal unset                 # clear sticky default
clawtool portal add <name>            # opens $EDITOR with a TOML template
clawtool portal remove <name>         # remove the [portals.<name>] block
clawtool portal ask [<name>] "<prompt>"
                                      # deferred until v0.16.2 (CDP driver)
```

MCP tool names: `PortalList` / `PortalWhich` / `PortalUse` /
`PortalUnset` / `PortalRemove` / `PortalAsk`. `PortalAdd` is
**CLI-only** because it spawns `$EDITOR`. After v0.16.2 lands, each
portal also exposes a per-name alias `<name>__ask` that wraps
`PortalAsk` so a model can call `my-deepseek__ask` directly.

## Worked example: chat.deepseek.com

### 1. Export your cookies from the browser

In Chrome / Edge / Brave install [EditThisCookie](https://www.editthiscookie.com)
or [Cookie-Editor](https://cookie-editor.com). Open
`https://chat.deepseek.com/` while logged in, click the extension,
choose **Export → JSON**. You'll get an array like:

```json
[
  {
    "name": "sessionid",
    "value": "REDACTED",
    "domain": ".deepseek.com",
    "path": "/",
    "secure": true,
    "httpOnly": true,
    "sameSite": "Lax"
  },
  {
    "name": "cf_clearance",
    "value": "REDACTED",
    "domain": ".deepseek.com",
    "path": "/",
    "secure": true,
    "httpOnly": true
  }
]
```

> The `httpOnly` flag is the critical reason cookies live in
> `secrets.toml` and ship via Chrome DevTools Protocol — JS
> `document.cookie` cannot set httpOnly cookies, so the simpler
> "inject via eval" path doesn't work for real session auth.

> **Wizard tip (v0.16.3+):** `clawtool portal add my-deepseek`
> spawns Chrome + captures cookies + selectors interactively — no
> manual export needed. The "export by hand" path below is for
> automation / non-TTY setups; it stays supported via
> `clawtool portal add --manual <name>`.

### 2. Add the portal (interactive wizard, default)

```sh
clawtool portal add my-deepseek
```

The wizard runs end-to-end:

1. Asks for the URL.
2. Spawns Chrome (your installed Chrome / Chromium / Brave / Edge,
   chromedp auto-detects) with `--headless=false` and a fresh temp
   profile so your normal login state stays untouched.
3. Prints a copy/paste prompt for the **Claude in Chrome** side
   panel (optional — log in manually if you don't have it). The
   prompt asks Claude to log you in and report the three CSS
   selectors.
4. After you confirm login, captures every cookie via
   `Network.getAllCookies` (httpOnly + secure included), filters
   to the portal's host, auto-detects auth-cookie names (httpOnly
   + `session*` / `auth*` / `*_token` patterns).
5. Asks for the input / submit / response selectors and a
   `response_done_predicate` template.
6. Writes `[portals.<name>]` to `config.toml` and the cookies JSON
   to `secrets.toml` under `[scopes."portal.<name>"]`.

### 2b. Add the portal manually (`--manual`)

If you can't use the interactive wizard (CI, no display, automation
script), pass `--manual`:

```sh
clawtool portal add --manual my-deepseek
```

This opens `$EDITOR` with a TOML template. Edit it to:

```toml
[portals.my-deepseek]
name = "my-deepseek"
base_url = "https://chat.deepseek.com/"
start_url = "https://chat.deepseek.com/"
secrets_scope = "portal.my-deepseek"
auth_cookie_names = ["sessionid", "cf_clearance"]
timeout_ms = 180000

[portals.my-deepseek.login_check]
type = "selector_exists"
value = "textarea"

[portals.my-deepseek.ready_predicate]
type = "selector_visible"
value = "textarea"

[portals.my-deepseek.selectors]
input = "textarea"
submit = "button[type='submit'], button[aria-label='Send']"
response = "[data-message-author-role='assistant'], div[class*='markdown']"

[portals.my-deepseek.response_done_predicate]
type = "eval_truthy"
value = """
(() => {
  const stop = document.querySelector('button[aria-label*="Stop"], button[data-testid*="stop"]');
  const messages = document.querySelectorAll('[data-message-author-role="assistant"], div[class*="markdown"]');
  const last = messages[messages.length - 1];
  return !stop && !!last && last.innerText.trim().length > 0;
})()
"""

[portals.my-deepseek.headers]
Accept-Language = "en-US,en;q=0.9"

[portals.my-deepseek.browser]
stealth = true
viewport_width = 1440
viewport_height = 1000
locale = "en-US"
```

Save and quit; clawtool validates and appends the block to
`~/.config/clawtool/config.toml`.

### 3. Store the cookies

Edit `~/.config/clawtool/secrets.toml` (mode 0600) and add:

```toml
[scopes."portal.my-deepseek"]
cookies_json = '''
[
  {"name":"sessionid","value":"REDACTED","domain":".deepseek.com","path":"/","secure":true,"httpOnly":true,"sameSite":"Lax"},
  {"name":"cf_clearance","value":"REDACTED","domain":".deepseek.com","path":"/","secure":true,"httpOnly":true}
]
'''
```

> `chmod 600 ~/.config/clawtool/secrets.toml` if the file isn't
> already locked down.

### 4. Drive it

```sh
clawtool portal use my-deepseek
clawtool portal ask "Refactor README.md for clarity"
```

`clawtool portal ask` (and `PortalAsk` MCP) spawn `obscura serve --port 0`
in the background, open a fresh CDP browser context (isolated cookie
jar via `disposeOnDetach`), seed the cookies + extra headers, navigate
to `start_url`, run `login_check` then `ready_predicate`, fill the
input selector with the prompt, click submit (or fall back to Enter
when no submit selector is configured), poll `response_done_predicate`
every 250ms until it returns truthy, and return the last response
selector's `innerText`. Progress lines stream to stderr; the captured
answer goes to stdout.

Inside `clawtool serve`, the same flow is wired through both the
generic `PortalAsk` MCP tool **and** a per-portal alias
`<name>__ask` (e.g. `my-deepseek__ask`). Aliases are computed at
server boot, so adding a portal then restarting `serve` makes the
new alias visible to the calling model — same lifecycle as
`clawtool source` aggregation.

## Predicate vocabulary

Three predicate types cover every chat portal we've looked at:

| `type` | `value` semantics |
| --- | --- |
| `selector_exists` | CSS selector; truthy when at least one match exists in the DOM. |
| `selector_visible` | CSS selector; truthy when a match exists AND `offsetParent != null`. |
| `eval_truthy` | JavaScript expression evaluated in-page via CDP `Runtime.evaluate`; result coerced to bool. |

Pick the cheapest one that works for the predicate in question:
prefer `selector_visible` for "is the textarea ready" and
`eval_truthy` for "is generation finished" (the latter usually
needs to inspect the absence of a "stop" button + the presence of a
non-empty last message).

## Failure modes (and what to do)

| Symptom | Cause | Fix |
| --- | --- | --- |
| `cookies missing required auth names: sessionid` | export missed the session cookie | re-export in the browser, replace `cookies_json` |
| `portal "x": secrets_scope must start with "portal."` | typo in `secrets_scope` | matches the prefix exactly: `portal.<name>` |
| `response_done_predicate` never fires | upstream changed selectors / button labels | inspect the page in DevTools, update the predicate |
| login_check fails on first nav | cookies expired | re-export from a fresh browser session |
| portal works once, then 403 | bot detection caught up | enable `[.browser] stealth = true`; if still blocked, the site doesn't tolerate automation, ToS doesn't permit it, accept it |

## Compile-time drivers (Bifrost, v0.22.65)

`portal list` surfaces two row kinds: config-stored
`[portals.<name>]` stanzas (saved web-UI targets) and
**compile-time drivers** — Go-native integrations registered at
binary build via `internal/portal/RegisterDriver`. Drivers don't
need a config block; they self-register and show up as a
discovery row.

The first driver landed in v0.22.65: a phase-1 stub for
[maximhq/bifrost](https://github.com/maximhq/bifrost), the
Apache-2.0 Go AI gateway that composes per-vendor portals under
one config (unified failover, semantic caching, budget
governance across OpenAI / Anthropic / Vertex / Bedrock / local
llama backends).

Today's surface:

```text
$ clawtool portal list
NAME             STATUS     SOURCE
my-deepseek      ready      config
bifrost          deferred   driver — Bifrost AI gateway: unified
                            failover, semantic cache, budget
                            governance (phase 2)
```

Phase-1 ships **registration only**. Calling `portal ask bifrost`
returns the typed `ErrBifrostDeferred` sentinel rather than
silently no-oping — the CLI matches via `errors.Is` and prints a
uniform deferred-feature message. Phase-2 lands the
`bifrost/core` Go module behind the `clawtool_bifrost` build tag
(it pulls a large transitive dep graph: every supported
provider's SDK + an embedded SQLite for the semantic cache, so
it's gated to keep the default binary small).

Operators wanting the real gateway: build with
`go build -tags clawtool_bifrost ./...` once phase-2 ships.

## Cross-references

- `docs/browser-tools.md` — `BrowserFetch` / `BrowserScrape`
  surface, install instructions for Obscura.
- `docs/http-api.md` — running the same surface over HTTP via
  `clawtool serve --listen :8080 --mcp-http`.
