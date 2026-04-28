# clawtool Sandbox

`clawtool sandbox` defines per-profile isolation for `clawtool send`
dispatches. This page is the operator-facing reference.

> **Status (v0.18):** surface ships today (`list` / `show` / `doctor`),
> profile parser is live, engine probes correctly identify bwrap /
> sandbox-exec / docker. The dispatch-time wrapping (`clawtool send
> --sandbox <profile>` actually constraining the upstream agent) lands
> incrementally — bwrap adapter v0.18.1, sandbox-exec v0.18.2, docker
> fallback v0.18.3.

## Why

Today `clawtool send` runs the upstream agent CLI in clawtool's own
process space — same filesystem, same network, same env. A
prompt-injection or model-side bug can read `~/.aws/credentials`,
exfiltrate, wipe disk. Sandbox profiles let the operator opt into
host-native isolation without touching their dispatch code.

We wrap an existing primitive — never reimplement seccomp /
AppContainer / namespaces.

## Engines

| OS | Primary | Fallback |
| --- | --- | --- |
| Linux | **bubblewrap** (`bwrap`) | Docker |
| macOS | **sandbox-exec** (Seatbelt) | Docker (Desktop) |
| WSL2 | **bubblewrap** | Docker |
| Windows | (v0.19) AppContainer + Job Objects | Docker (Desktop) |
| Anywhere | **noop** (no enforcement, surface only) | — |

Install hints when the engine is missing:

```sh
# Debian/Ubuntu
sudo apt install bubblewrap

# macOS — sandbox-exec is built-in. No install needed.

# Anywhere
brew install bubblewrap         # Homebrew (Linux/macOS)
```

## CLI

```text
clawtool sandbox list                List configured profiles + engine.
clawtool sandbox show <name>         Render parsed profile + engine binding.
clawtool sandbox doctor              Probe engines on this host.
clawtool sandbox run <name> -- <cmd> Escape hatch — one-off sandboxed cmd.
                                     (Engine enforcement v0.18.1+.)

clawtool send --sandbox <name> "<prompt>"
                                     Wrap dispatch to the resolved agent
                                     in the named profile. Per-call;
                                     overrides any per-agent default.
```

MCP tools: `SandboxList`, `SandboxShow`, `SandboxDoctor`. `SandboxRun`
is intentionally CLI-only — letting a model spawn arbitrary
sandboxed commands has the wrong default.

## Profile schema

`[sandboxes.<name>]` in `~/.config/clawtool/config.toml`:

```toml
[sandboxes.workspace-write-with-net]
description = "Write only the current repo, talk only to the three model APIs."

# Filesystem rules. mode is "ro" | "rw" | "none".
paths = [
  { path = ".",                 mode = "rw" },
  { path = "/etc/ssl/certs",    mode = "ro" },
  { path = "/etc/resolv.conf",  mode = "ro" },
  { path = "/tmp",              mode = "rw" },
  { path = "${HOME}/.cache/clawtool", mode = "rw" },
]

[sandboxes.workspace-write-with-net.network]
policy = "allowlist"            # none | loopback | allowlist | open
allow = [
  "api.openai.com:443",
  "api.anthropic.com:443",
  "generativelanguage.googleapis.com:443",
]

[sandboxes.workspace-write-with-net.limits]
timeout       = "5m"
memory        = "1GB"
cpu_shares    = 1024
process_count = 32

[sandboxes.workspace-write-with-net.env]
allow = [
  "PATH", "HOME", "LANG", "LC_ALL", "TERM",
  "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
]
deny = ["AWS_*", "GH_TOKEN"]
```

## Per-agent default

Pin a profile to an agent so every dispatch through that instance
goes through the sandbox without `--sandbox`:

```toml
[agents.codex]
family = "codex"
sandbox = "workspace-write-with-net"
```

Resolution precedence: per-call `--sandbox` flag > `[agents.X].sandbox`
> global default > none.

## Native flag composition (v0.18.1+)

Codex / Claude Code / Gemini each have their own native sandbox /
permission flags. clawtool's external sandbox **wraps** them — both
layers compose, and the effective permission is the intersection.
The profile can opt into the upstream's native flag too:

```toml
[sandboxes.workspace-write-with-net.native]
codex  = { sandbox = "workspace-write" }
claude = { permission_mode = "acceptEdits" }
gemini = { sandbox = true, approval_mode = "auto_edit" }
```

Why both? The upstream's flag controls model-generated commands;
clawtool's external sandbox protects the host from bugs in the
agent's own runtime / dependencies. Defense in depth.

## When the engine is missing

`sandbox doctor` reports availability. When `selected: noop`:

```text
ENGINE           AVAILABLE
bwrap            no
docker           no
noop             yes

selected: noop
  install bubblewrap (Linux) / sandbox-exec (macOS, built-in) / Docker for real enforcement
```

The dispatcher logs a warning + runs unwrapped. Set
`fail_if_unavailable = true` in the profile when unsandboxed
dispatch is unacceptable — the dispatch then errors rather than
silently bypassing the sandbox.

## Cross-references

- `internal/sandbox/` — package implementation.
- `docs/portals.md`, `docs/browser-tools.md` — neither composes
  with sandbox in v0.18; portals run in the operator's own
  Chrome (wizard) or Obscura (runtime), browser tools call
  Obscura directly. Sandbox is for `clawtool send` agent
  dispatches.
