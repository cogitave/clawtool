# Promptfoo — setup playbook

Connects clawtool's BIAM agent surface to [promptfoo](https://github.com/promptfoo/promptfoo),
an LLM eval / red-team harness. The recipe ships a baseline
`promptfooconfig.yaml` that drives every clawtool agent family
through the BIAM dispatch path
(`clawtool send --agent <family>`) so jailbreak / prompt-
injection probes hit the *real* dispatch surface — not a mock.

## When to use this

- **Pre-merge for clawtool itself** — if you're contributing to
  the BIAM transport layer or onboarding a new agent peer, run
  promptfoo before merging. The baseline catches regressions in
  prompt-injection resistance.
- **Per-project for downstream operators** — if your project
  exposes the agent to operator-controlled prompts (web UI,
  Slack bot, cron-fed RSS), apply the recipe to your repo and
  add project-specific probes alongside the baseline.

The recipe is `Core: true` — `clawtool init` applies it by
default — but Beta-stability, so it can change shape until
soak finishes.

## Prerequisites

- Node.js 20+ (`node --version`). promptfoo is npm-distributed.
- API keys for the model providers you'll evaluate (Anthropic,
  OpenAI, Google) exported as `ANTHROPIC_API_KEY`,
  `OPENAI_API_KEY`, `GEMINI_API_KEY`. promptfoo reads each
  provider's standard env var.
- A repo with clawtool already configured — promptfoo invokes
  `clawtool send --agent ...` so the agent surface must answer.

## Step 1 — install promptfoo

```bash
npm install -g promptfoo
promptfoo --version
```

(`npx promptfoo@latest ...` works too if you'd rather not install
globally.)

## Step 2 — scaffold the redteam config

```bash
clawtool recipe apply promptfoo-redteam
```

This drops `promptfooconfig.yaml` at the repo root. The file:
- Names every clawtool agent family as a target provider via
  `exec: clawtool send --agent <family>`.
- References standard jailbreak corpora by canonical nickname
  (no copyrighted probes embedded — promptfoo fetches them
  itself).
- Sets a baseline pass-rate the redteam run is expected to clear.

## Step 3 — run the redteam suite

```bash
promptfoo redteam run
```

What this does:
- promptfoo enumerates every probe in the configured corpora.
- For each probe, it shells out to
  `clawtool send --agent <family>` once per family.
- It captures the reply, runs assertions (refusal-pattern match,
  no-PII-leak, no-secret-leak), and tallies pass/fail.
- A per-family report lands in `./promptfoo-redteam-output/`.

The full suite takes ~10-30 minutes depending on probe count
and provider latency.

## Step 4 — interpret the report

```bash
promptfoo redteam report
```

Opens an interactive HTML report in the browser. Look for:
- **Refusal rate per family** — a healthy peer refuses
  ≥95% of probes. A drop indicates a regression.
- **Per-probe deltas vs the previous run** — promptfoo persists
  history; a probe that newly passes (or newly fails) names the
  exact prompt + completion so you can drill in.
- **Per-family false-positives** — sometimes a peer refuses a
  legitimate prompt because a probe corpus contains it. Tag
  those for follow-up.

## Troubleshooting

- **`clawtool: command not found`** — promptfoo runs from the
  repo root and inherits your `$PATH`. Confirm `which clawtool`
  in the same shell before re-running.
- **Provider rate-limit errors** — promptfoo doesn't pace by
  default. Add `delay: 1000` (ms) per provider in the config to
  throttle.
- **Suite runs but every probe fails** — most often the
  `clawtool send` invocation is failing (e.g. agent not
  configured). Run a single dispatch by hand:
  `clawtool send --agent claude "ping"` and confirm a reply
  comes back before re-running redteam.
- **Report doesn't open** — `promptfoo redteam report --port
  8080` and visit the printed URL manually.

---

**Read by**: agents, on operator request.
**Maintained at**: `playbooks/promptfoo/setup.md`.
**Auth flows covered**: per-provider API-key env vars.
