# Security Policy

## Reporting a vulnerability

**Do not** file a public issue, PR, or Discussion for security-sensitive findings.

Email **bahadirarda96@gmail.com** with subject line starting `[clawtool security]`. Include:

- A description of the vulnerability and its impact (data exfiltration, code execution, privilege escalation, etc.).
- The clawtool version, OS, and any source/instance config relevant to the reproduction.
- Step-by-step reproduction or a proof-of-concept (be conservative — minimum disclosure that lets us reproduce).
- Whether you intend to disclose publicly, and on what timeline.

You'll get an acknowledgement within **3 business days** and a fix or mitigation plan within **14 days** for confirmed issues.

## Scope

In scope:
- The clawtool binary (`cmd/clawtool`) and every package under `internal/`.
- The release artifacts published from `.github/workflows/release.yml`.
- Documentation that misleads users into insecure configurations.

Out of scope (handle upstream):
- Vulnerabilities in wrapped engines (`pdftotext`, `pandoc`, `ripgrep`, `excelize`, `go-readability`, `bleve`, `mark3labs/mcp-go`, …). Report to those upstream projects; we'll bump the dep once they fix.
- Third-party MCP source servers added via `clawtool source add`. Report to the source's own security contact.
- Misconfiguration that follows the docs but the user disagrees with (e.g. the user enabled `Bash` and is surprised it can run shell commands).

## Hardened defaults clawtool relies on

These are invariants we will treat as security bugs if violated:

- `~/.config/clawtool/secrets.toml` is created with mode `0600`. The Save path is atomic temp+rename.
- `Bash` runs with process-group SIGKILL on context cancel so a runaway child cannot hold open the captured pipes. Output is preserved up to the kill point.
- `Read` refuses files containing NUL bytes; `Edit` and `Write` apply the same rule symmetrically.
- `WebFetch` rejects schemes other than `http://` / `https://`. Body capped at 10 MiB.
- `WebSearch` reads its API key from secrets store first, env second; the key is never echoed in tool output.
- `Edit` refuses ambiguous matches by default to prevent accidental wide-scope rewrites.

## Disclosure timeline

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure):

1. Reporter sends email.
2. Maintainer acknowledges + opens private discussion.
3. Fix lands on a private branch, then a coordinated public release.
4. Post-release we publish a `chore(security): …` commit and a release note crediting the reporter (unless they prefer anonymity).

## Bug bounty

clawtool does not currently run a paid bounty program. We'll credit reporters in the changelog and release notes.
