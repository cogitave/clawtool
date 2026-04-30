# GitHub — setup playbook

Connects clawtool's agent surface to GitHub.com (or GitHub
Enterprise) via the `gh` CLI's OAuth device flow. No personal
access token in env, no plaintext secret in `~/.clawtool/`.

## When to use this vs `clawtool source add github`

- **`source add github`** spins up the official
  `@modelcontextprotocol/server-github` MCP server and exposes
  typed tools (`mcp__clawtool__github__create_issue`,
  `mcp__clawtool__github__get_pr`, …). Pick this when the agent
  benefits from typed tool schemas.

- **This playbook** has the agent shell out to `gh` directly.
  Pick this when:
  - The MCP source isn't available (offline laptop, GitHub
    Enterprise instance the upstream server doesn't yet
    support).
  - You want the agent to use **your** existing `gh` session
    (so audit logs name you, not a service account).
  - You want zero clawtool-side credential storage — `gh`
    handles its own token in `~/.config/gh/`.

Both paths can coexist. Operator picks per-task.

## Prerequisites

- `gh` CLI installed (`brew install gh`, `winget install
  GitHub.cli`, or `apt install gh`).
- A GitHub account with access to the repos you want the agent
  to drive.

## Step 1 — ask the URL first

If you use **github.com**, skip to Step 2. If you use **GitHub
Enterprise**, the agent asks:

> What's your GitHub Enterprise hostname? (e.g.
> `github.acme.com`)

The hostname pins which auth host `gh` writes its token under.

## Step 2 — run `gh auth login`

For github.com:
```bash
gh auth login --hostname github.com --git-protocol https --web
```

For GitHub Enterprise:
```bash
gh auth login --hostname <your-host> --git-protocol https --web
```

`gh` prints a one-time code, opens the operator's default
browser, and waits for the operator to complete the OAuth
device flow. The token lands in `~/.config/gh/hosts.yml`.

## Step 3 — validate

Round-trip against the live host:

```bash
gh api user --jq '.login'
```

This should print the operator's GitHub username. If it errors,
the most common causes are:
- `gh` not on PATH (re-open the shell).
- Browser flow was abandoned; re-run Step 2.
- The hostname in `gh auth status` doesn't match — pass
  `--hostname <host>` explicitly.

Once `gh api user` succeeds, every future agent session can use
`gh` without re-authenticating until the token is revoked.

## Step 4 — what the agent can do now

The agent calls `mcp__clawtool__Bash` with `gh` invocations:

| Intent | Command |
|---|---|
| Read a PR | `gh pr view <number> --json title,body,state` |
| List open issues | `gh issue list --state open --json number,title,author` |
| Open an issue | `gh issue create -t "<title>" -b "<body>"` |
| Comment on a PR | `gh pr comment <number> --body "<text>"` |
| Search repos | `gh search repos --json fullName "<query>"` |
| Read a file | `gh api repos/<owner>/<repo>/contents/<path> --jq '.content' \| base64 -d` |

For full surface, see `gh help` — every read/write the operator
can do via the GitHub web UI is available via `gh` (the same
identity, the same audit trail, the same scopes).

## Step 5 — connection-sso.md (placeholder)

If your GitHub Enterprise instance uses SAML SSO via Okta /
Microsoft Entra, `gh auth login --web` typically still works:
the browser flow takes the operator through the SSO
challenge, and `gh` receives the OAuth-bridged token. If your
admin disabled OAuth device flow, fall back to a fine-grained
PAT — see `connection-pat.md` (not yet shipped; opens an issue
for the first GitHub Enterprise + PAT user).

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/github/setup.md`.
**Auth flows covered**: OAuth device flow via `gh auth login`.
