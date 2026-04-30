// Package rules — rtk-rewrite helper.
//
// rtk (https://github.com/rtk-ai/rtk, Apache-2.0) is a CLI proxy
// that compresses common Bash command output before it reaches the
// LLM context — measured 60-90% token savings on `git status`,
// `ls -R`, `grep`, etc. Wrapping every shell call in `rtk` is one
// of the highest-leverage knobs for cutting context burn on long
// agent loops.
//
// This file ships the rewrite primitive. Callers (the future
// pre_tool_use dispatch site, RulesCheck, or any other surface
// that wants to opt a Bash invocation through rtk) call
// RewriteBashCommand(cmd) and dispatch the returned string instead
// of the original. Three short-circuits keep the rewrite safe:
//
//  1. rtk must exist on PATH — exec.LookPath is memoized via
//     sync.Once so the lookup runs once per process. If rtk is
//     missing, the rewrite is a no-op (return the original cmd
//     unchanged). This is a feature: the pre_tool_use rule that
//     calls RewriteBashCommand never blocks dispatch — it just
//     skips the optimisation.
//
//  2. The first whitespace-delimited token must be in the
//     allowlist (`git`, `ls`, `grep`, ...). Anything else (curl,
//     docker, npx, ssh) passes through unchanged so we don't
//     accidentally pipe a stream-producing or interactive command
//     through a buffering proxy.
//
//  3. Already-prefixed commands (`rtk git status`) are detected
//     and returned unchanged so a chain of rules calling the
//     helper is idempotent.
//
// The allowlist is hard-coded here as the canonical default. A
// future iteration can read .clawtool/rtk-rewrite-list.toml to
// let operators extend it; for now keep the surface tight so the
// recipe-shipped TOML is a documentation artifact rather than a
// behavioural divergence.

package rules

import (
	"os/exec"
	"strings"
	"sync"
)

// rtkAllowlist is the set of commands we KNOW rtk compresses
// safely. Mirrors the recipe's default rtk-rewrite-list.toml.
// Adding a command here means: (a) it's read-only / mostly read-
// only, (b) its output is tabular / line-oriented (rtk's sweet
// spot), (c) it doesn't interactively prompt or stream.
var rtkAllowlist = map[string]struct{}{
	"git":   {},
	"ls":    {},
	"grep":  {},
	"cat":   {},
	"head":  {},
	"tail":  {},
	"find":  {},
	"tree":  {},
	"diff":  {},
	"stat":  {},
	"wc":    {},
	"awk":   {},
	"sed":   {},
	"rg":    {},
	"jq":    {},
	"yq":    {},
	"echo":  {},
	"which": {},
}

// rtkLookup memoizes the PATH probe so the cost is paid once per
// process. The result is captured via sync.Once and reused on every
// subsequent rewrite call — important because the dispatch site can
// fire dozens of rewrites per agent turn.
var (
	rtkLookupOnce sync.Once
	rtkAvailable  bool
)

// rtkOnPath reports whether `rtk` is reachable via exec.LookPath.
// The probe runs at most once per process; subsequent calls return
// the cached verdict.
func rtkOnPath() bool {
	rtkLookupOnce.Do(func() {
		_, err := exec.LookPath("rtk")
		rtkAvailable = err == nil
	})
	return rtkAvailable
}

// RewriteBashCommand returns cmd with `rtk ` prepended when:
//   - cmd's first whitespace-delimited token is in the allowlist;
//   - rtk is on PATH (memoized).
//
// Otherwise cmd is returned unchanged. The function is pure modulo
// the one-time PATH probe — no I/O on the hot path.
//
// Already-rewritten commands (`rtk git status`) are detected by
// looking at the first token and returned unchanged.
func RewriteBashCommand(cmd string) string {
	first := firstToken(cmd)
	if first == "" {
		return cmd
	}
	// Idempotency: if rtk is already the leading token, skip.
	if first == "rtk" {
		return cmd
	}
	if _, ok := rtkAllowlist[first]; !ok {
		return cmd
	}
	if !rtkOnPath() {
		return cmd
	}
	return "rtk " + cmd
}

// firstToken returns the first whitespace-delimited word of cmd,
// trimming leading whitespace. Returns "" for empty / whitespace-
// only input. We intentionally don't shell-parse: agent tool calls
// arrive as a single command string, and the allowlist is plain
// command names with no embedded paths.
func firstToken(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if i := strings.IndexAny(cmd, " \t\n"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// resetRtkLookupForTest forces the next rtkOnPath call to re-probe
// PATH. Test-only — production code never calls this. Lets a unit
// test toggle PATH between cases without process restart.
func resetRtkLookupForTest() {
	rtkLookupOnce = sync.Once{}
	rtkAvailable = false
}
