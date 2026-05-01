# clawtool telemetry

Anonymous, opt-in PostHog event emission. The contract is
strict: **no prompts, no paths, no file contents, no secrets,
no env values, no hostnames, no instance IDs**. Every event
property must be in a hard-coded allow-list; anything else is
silently dropped before it reaches the wire.

This page documents the operator-facing surface — env-var kill
switches, what gets emitted, what doesn't — and the v0.22.81
CI-noise fix.

## Kill switches

The simplest way to turn telemetry off, in priority order:

```sh
# Process-level: env var, takes effect immediately on next boot.
export CLAWTOOL_TELEMETRY=0          # also: false / off
```

`SilentDisabled()` returns true when `CLAWTOOL_TELEMETRY` is
exactly `0`, `false`, or `off` (case-sensitive). Daemon and
CLI both check this gate before constructing the client. The
operator wanted this for "before talking on conference Wi-Fi"
moments where flipping a config file is too slow.

```sh
# Persistent: config.toml — survives restarts.
clawtool telemetry off
```

This sets `[telemetry] enabled = false` in
`~/.config/clawtool/config.toml`.

> **Pre-v1.0 lock:** while `version.Resolved()` reports a
> major-zero version, `cfg.Enabled = false` is force-overridden
> to true at boot with a stderr warning. Anonymous telemetry is
> the funnel-diagnostic data the project can't afford to lose
> while the shape is still settling. The lock disengages
> automatically the moment v1.0.0 is tagged. The env-var kill
> switch (`CLAWTOOL_TELEMETRY=0`) IS still honored under the
> lock — the override is a config-only policy.

## CI emit gate (v0.22.81)

CI runners pollute the production analytics project — ~95% of
events the operator pulled on 2026-04-30 came from CI hosts
running pseudo-version Go-cached binaries. The CI gate (added
in v0.22.81) makes telemetry default-off when:

1. `detectCI()` reports a CI environment (standard env vars:
   `CI`, `GITHUB_ACTIONS`, `GITLAB_CI`, `BUILDKITE`, etc.), AND
2. `CLAWTOOL_TELEMETRY_FORCE_CI` is NOT set to `1` / `true` /
   `on`.

The maintainer opt-in re-enables emission for cogitave's own
release-tracking workflows that legitimately want to send
events from CI:

```sh
# In a CI workflow that should emit telemetry (cogitave-internal):
export CLAWTOOL_TELEMETRY_FORCE_CI=1
```

Every other downstream CI runner stays silent without
configuration. Implementation: `telemetry.CIDisabled()`. Read
live (not cached at package init) so a single daemon's
lifetime can manipulate the env via `t.Setenv` in tests
without re-instantiating the package.

## What gets emitted

Five event kinds carry the full surface:

| Event | Fired from | Cadence |
| --- | --- | --- |
| `clawtool.install` | Daemon boot, once per host (marker file under `$XDG_DATA_HOME/clawtool/install-emitted` blocks repeats) | Once |
| `clawtool.host_fingerprint` | Daemon boot | Per daemon boot |
| `server.start` / `server.stop` | `clawtool serve` lifecycle | Per daemon boot / shutdown |
| `cli.command` | Every CLI verb invocation | Per `clawtool <verb>` call |
| `clawtool.update_check` | Periodic update probe | Hourly |
| `clawtool.daemon.log_event` | `LogWatcher` tailing the daemon log | Rate-limited (60/min cap) |

Event payloads are restricted to the keys in the
`allowedKeys` map in `internal/telemetry/telemetry.go`. The
short list:

- **CLI dimensions**: `command`, `subcommand`, `flags` (CSV of
  feature toggles only — never values), `outcome`,
  `duration_ms`, `exit_code`, `error_class`.
- **Per-event taxonomies**: `agent` (family name only, never
  instance ID), `bridge`, `recipe`, `engine`,
  `install_method`, `update_outcome`, `transport`, `severity`.
- **Host fingerprint** (single `clawtool.host_fingerprint`
  event, all values bucketed):
  - `cpu_count`, `mem_tier` (`<2GB` / `2-8GB` / `8-32GB` /
    `>32GB` / `unknown`), `go_version`.
  - `container`, `is_ci`, `is_wsl`, `term_kind` (`tty` /
    `ssh` / `ci` / `headless`).
  - `locale_lang` (first segment of `$LANG`, e.g. `tr` / `en`;
    `unknown` on parse fail).
  - `claude_code_present`, `codex_present`,
    `gemini_present`, `opencode_present` (boot-time PATH
    probe, presence-bool only).
  - `posthog_reachable`, `github_reachable` (best-effort TCP
    probe, capped at 1 s).
- **PostHog conventions**: `$session_id`, `$session_start`,
  `$session_end`, `$lib`, `$lib_version`, `$geoip_disable`
  (always set so PostHog doesn't auto-stamp city / country
  from the request IP).

## What's NOT emitted

By construction, the wire never carries:

- Prompt text or model output.
- File paths (yours, ours, anyone's).
- Env values (`PATH`, `HOME`, secret-shaped strings).
- Hostnames or absolute IPs.
- Instance IDs of agents (only the family name — `claude`,
  `codex`, `gemini`, `opencode`, `hermes`).
- Repo names, branch names, commit SHAs.
- Tool-call arguments. The CLI dispatcher strips arg slices
  before passing to `Track`; only the verb + sub-verb +
  feature-flag set survive.

`TestFingerprintProps_NoSensitiveContent` makes the negative
assertion explicit — no value in the fingerprint event may
contain user-identifiable text. Run it against a representative
host before adding a new dimension:

```sh
go test ./internal/telemetry/ -run TestFingerprintProps_NoSensitiveContent
```

## Anonymous distinct ID

Each host has one `distinct_id` — a 16-byte random hex token
generated on first boot and stored at
`$XDG_DATA_HOME/clawtool/telemetry-id` (mode 0600). It is NOT
derived from MAC / hostname / username / any host-identifying
fingerprint. Re-installs reuse the existing token; deleting
the file forces a fresh ID on next boot.

`$session_id` is regenerated per daemon / CLI invocation so a
restart starts a new session — the right boundary for CLI
tools.

## Forwarded daemon log events

`LogWatcher` (in `internal/telemetry/logwatch.go`) tails the
daemon log starting from EOF, classifies lines into severity
+ event_kind taxonomies, redacts known secret shapes, and
emits `clawtool.daemon.log_event` with classification fields
ONLY — never the log line body itself. Rate-limited at 60
events/minute so a panicking daemon can't flood PostHog.

Severities: `error` / `warn` / `panic`.
Event kinds: `panic` / `fatal` / `biam` / `auth` / `io` /
`other`.

## Operator overrides

To route telemetry to a self-hosted PostHog instance instead
of cogitave's project:

```toml
# ~/.config/clawtool/config.toml
[telemetry]
enabled = true
api_key = "phc_<your-project-key>"
host    = "https://your-posthog.example.com"
```

Empty `api_key` falls back to the embedded cogitave default
(same convention as posthog-js shipping a public client-side
key in browser bundles); operators who want events to land
somewhere else override it explicitly.

## Cross-references

- `internal/telemetry/telemetry.go` — `Track` / `New` / kill
  switches.
- `internal/telemetry/fingerprint.go` — host fingerprint
  collector.
- `internal/telemetry/logwatch.go` — daemon-log forwarder.
- `internal/telemetry/telemetry_test.go` — pre-v1.0 lock,
  CI-gate, and allow-list tests.
