# Meta-playbook — connect any tool

Use this when the tool you want to connect isn't in
`playbooks/<tool>/`. Follows the 10xProductivity convention
(ZhixiangLuo): research auth → ask the URL FIRST → try the most
likely auth → ask only for missing credentials → validate →
write → optional PR.

## Steps the agent runs

### 1. Ask the URL first

Before guessing, ask the operator:

> Which URL do you log in to for `<tool>`? (e.g.
> `https://acme.atlassian.net` for Jira Cloud, vs.
> `https://jira.acme.com` for Jira Server.)

The URL pins down deployment variant (Cloud vs Server vs
self-hosted) and downstream auth mechanism.

### 2. Research auth — but don't ask the operator yet

Drive the URL once with `mcp__clawtool__BrowserFetch` (or `curl`
via `Bash` for plain APIs). Look for:

- Login page redirects (Okta? Google? Microsoft Entra? Generic
  SSO?).
- API surface: REST? GraphQL? gRPC? An OpenAPI spec exposed at
  `/api/openapi.json`?
- A CLI: does `gh auth login`-style tooling exist? `aws
  configure`, `gcloud auth login`, `op signin`, `npx
  @scope/cli`?
- Docs page mentioning "personal access token" / "API key" /
  "OAuth app".

Most tools have a hierarchy of auth paths. Prefer in this order:

1. **Existing CLI with OAuth device flow** (`gh auth login`,
   `gcloud auth login`, `op signin`) — agent shells out, the
   operator clicks through once in their browser, the CLI stores
   the token. Zero plaintext credentials in clawtool.
2. **Personal Access Token in the operator's OS keychain** —
   stored via `clawtool source set-secret` if the tool already
   has an MCP source variant, otherwise via `clawtool secrets`.
3. **Browser session reused via `clawtool portal add`** — for
   tools without API or CLI surface, drive the rendered SPA via
   chromedp + saved cookies.
4. **Bare API key in env** — last resort, document the
   provenance and rotation plan in the playbook so the operator
   can rotate when the key inevitably leaks.

### 3. Try the most likely auth

Pick the highest-priority path that matches the tool. Try a
no-op verification call (e.g. `gh api user`, `curl
$URL/api/v1/me`) and only ask the operator for the next missing
credential if the call fails.

The principle: **don't gather credentials you might not need**.
Each prompt to the operator costs trust + time.

### 4. Validate

Validation MUST be a real round-trip against the live URL the
operator gave. No copy-paste from docs. The
`contributing.md`-style "run before you write" rule from
10xProductivity applies: every snippet in the new playbook must
be a snippet you actually executed and saw succeed.

### 5. Write the playbook

File layout:

```
playbooks/<tool>/
  setup.md                ← orchestration entry point
  connection-<auth>.md    ← one per auth path you proved
```

`setup.md` should:
- Begin with a one-paragraph "what this connects" preamble.
- List the prerequisites (CLI installed? account exists?).
- Branch on the auth path: "if your tenant uses Okta SSO →
  follow `connection-sso.md`; otherwise → follow
  `connection-pat.md`".
- End with a validation step that's a real, copy-pasteable
  command the operator can run to confirm the connection.

`connection-<auth>.md` should:
- Be self-contained: clone-and-run with no other docs needed.
- Specify the exact CLI / browser steps in numbered order.
- Include the validation snippet at the end.
- Not embed live secrets; reference `clawtool source
  set-secret` / OS keychain paths instead.

### 6. Decide where it lives

- **Open-source tool** the broader community could benefit from
  → write under `playbooks/<tool>/`, optional PR upstream.
- **Internal / proprietary tool** specific to your org →
  write under `playbooks/_personal/<tool>/` (gitignored,
  stays on your machine).

Both paths use the **same setup template**. The only difference
is which directory the file lands in.

### 7. Optional: PR back

For open-source / commercial tools, the playbook is potentially
useful to others. The PR bar is:
- Multi-environment validation (e.g. Jira Cloud AND Jira Server
  if the tool ships both).
- Snippets that the contributor actually executed (not adapted
  from upstream docs).
- A `connection-<auth>.md` per auth path that's been verified
  end-to-end.

Internal-tool playbooks under `_personal/` never PR upstream —
that's the whole point of the gitignore. The playbook
**convention** is open source; the playbook **content** stays
private.
