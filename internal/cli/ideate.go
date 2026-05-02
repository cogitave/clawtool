// Package cli — `clawtool ideate` subcommand. Top of the
// three-layer self-direction stack:
//
//	Ideator   — what to work on (this verb)
//	Autopilot — when to work on it (`clawtool autopilot accept`)
//	Autonomous — how to work on it (`clawtool autonomous "<goal>"`)
//
// `ideate` surveys cheap repo-local signals (open ADR questions,
// TODOs, recent CI failures, manifest drift, BM25 baseline drift)
// and prints ranked Idea candidates. With --apply, every selected
// Idea is pushed onto the autopilot backlog at status=proposed.
// Operator approval (`clawtool autopilot accept <id>`) flips
// proposed → pending; only then does AutopilotNext claim it. Without
// that gate the agent could silently drive its own autonomous
// pipeline past human review.
//
// This file owns the CLI parsing + edge wiring of default sources;
// the orchestrator + IdeaSource interface live in
// internal/ideator/, the concrete sources in
// internal/ideator/sources/. Splitting defaults into the edge
// avoids an import cycle between ideator and its sources.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/cogitave/clawtool/internal/autopilot"
	"github.com/cogitave/clawtool/internal/ideator"
	"github.com/cogitave/clawtool/internal/ideator/sources"
	"github.com/cogitave/clawtool/internal/tools/core"
	"github.com/cogitave/clawtool/internal/tools/registry"
)

// init wires the canonical manifest builder into the manifest_drift
// source registry. The package-global slot lets the source stay
// import-cycle-free; this init call is the one place the edge knows
// about both core (BuildManifest) and sources (the slot).
func init() {
	sources.SetDefaultManifestProvider(func() *registry.Manifest {
		return core.BuildManifest()
	})
}

const ideateUsage = `Usage:
  clawtool ideate [flags]
                                  Survey repo signals and print Idea candidates.
                                  Read-only by default. Use --apply to push
                                  selected ideas into the autopilot backlog
                                  at status=proposed (operator runs
                                  'clawtool autopilot accept <id>' to flip
                                  proposed → pending).

  clawtool ideate --apply [flags]
                                  Run plus push to autopilot. Each surviving
                                  Idea becomes one proposed backlog item.

  clawtool ideate --baseline-set
                                  Read /tmp/clawtool-toolsearch-bench.tsv,
                                  compute the rank-1 BM25 hit rate, and write
                                  it to ~/.config/clawtool/ideator/bench-baseline.json.
                                  bench_regression source compares against
                                  this baseline on subsequent ideate runs.

Flags:
  --top N                         Cap on returned ideas (default 10).
  --source <name>                 Restrict to one source (adr_questions,
                                  adr_drafting, todos, ci_failures,
                                  manifest_drift, bench_regression,
                                  deadcode_hits, deps_outdated).
  --format text|json              Print format (default text).
  --apply                         Push selected ideas to the autopilot
                                  backlog at status=proposed.
  --baseline-set                  Write the current bench TSV as the new
                                  baseline (no other action).
  --repo <path>                   Repo root to scan (default cwd).

Sources:
  adr_questions     wiki/decisions/*.md "## Open questions" blocks.
  adr_drafting      wiki/decisions/*.md ADRs in 'drafting' status > 30 days.
  todos             TODO / FIXME / XXX comments in *.go files.
  ci_failures       Recent failed GitHub Actions runs (gh run list).
  manifest_drift    MCP tool description vs registered description.
  bench_regression  ToolSearch BM25 rank-1 hit-rate baseline diff.
  deps_outdated     Outdated Go module dependencies (go list -m -u).
  vuln_advisories   Go security advisories (govulncheck -json ./...).
  stale_files       .go files untouched > N days (heuristic review).
  pr_review_pending Open PRs awaiting review > N hours (gh pr list).
  stale_branches    Remote branches whose tip is already merged into default.

Stack:
  ideate → autopilot accept → autopilot next → autonomous
  Discover → Approve   → Claim → Iterate
`

func (a *App) runIdeate(argv []string) int {
	fs := flag.NewFlagSet("ideate", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	top := fs.Int("top", 10, "Cap on returned ideas.")
	source := fs.String("source", "", "Restrict to one source.")
	format := fs.String("format", "text", "Output format: text | json.")
	apply := fs.Bool("apply", false, "Push selected ideas to autopilot at status=proposed.")
	baselineSet := fs.Bool("baseline-set", false, "Write current bench TSV as baseline; no other work.")
	repoFlag := fs.String("repo", "", "Repo root to scan (default cwd).")
	help := fs.Bool("help", false, "Show usage.")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *help {
		fmt.Fprint(a.Stdout, ideateUsage)
		return 0
	}

	if *baselineSet {
		return a.runIdeateBaselineSet()
	}

	repoRoot := *repoFlag
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(a.Stderr, "clawtool ideate: cwd: %v\n", err)
			return 1
		}
		repoRoot = cwd
	}

	opts := ideator.Options{
		RepoRoot:     repoRoot,
		SourceFilter: *source,
		TopK:         *top,
		Sources:      defaultIdeatorSources(),
		Warn:         a.Stderr,
		// CLI invocations are operator-driven: an empty result
		// should print "no ideas" honestly. The dry-loop diagnostic
		// is for the autopilot/MCP path where the loop must keep
		// producing work; suppressing here keeps `clawtool ideate`
		// truthful when the operator is asking by hand.
		SuppressDryDiagnostic: true,
	}

	ctx := context.Background()
	var (
		res ideator.RunResult
		err error
	)
	if *apply {
		res, err = ideator.RunAndQueue(ctx, opts, autopilot.Open())
	} else {
		res, err = ideator.Run(ctx, opts)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool ideate: %v\n", err)
		return 1
	}

	if *format == "json" {
		body, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(a.Stdout, string(body))
		return 0
	}
	a.printIdeasText(res, *apply)
	return 0
}

func (a *App) runIdeateBaselineSet() int {
	bl, err := sources.SaveBaseline("", "")
	if err != nil {
		fmt.Fprintf(a.Stderr, "clawtool ideate --baseline-set: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Stdout, "baseline written: hit_rate=%.2f num_queries=%d at=%s\n",
		bl.HitRate, bl.NumQueries, sources.DefaultBenchBaselinePath())
	return 0
}

func (a *App) printIdeasText(res ideator.RunResult, applied bool) {
	if len(res.Ideas) == 0 {
		fmt.Fprintln(a.Stdout, "(no ideas — every source returned empty)")
	} else {
		for i, idea := range res.Ideas {
			fmt.Fprintf(a.Stdout,
				"%2d. [%s prio=%d] %s\n    evidence: %s\n",
				i+1, idea.SourceName, idea.SuggestedPriority,
				idea.Title, idea.Evidence)
			if idea.Summary != "" {
				fmt.Fprintf(a.Stdout, "    summary:  %s\n", idea.Summary)
			}
		}
	}
	// Per-source rollup so the operator sees what each source emitted
	// (zeros included — useful to spot a quietly broken source).
	if len(res.PerSource) > 0 {
		fmt.Fprintln(a.Stdout)
		fmt.Fprintln(a.Stdout, "per-source counts:")
		for _, name := range orderedSourceNames(res.PerSource) {
			fmt.Fprintf(a.Stdout, "  %-18s %d\n", name, res.PerSource[name])
		}
	}
	if applied {
		fmt.Fprintln(a.Stdout)
		fmt.Fprintf(a.Stdout, "queued: added=%d deduped=%d skipped=%d\n",
			res.Added, res.Deduped, res.Skipped)
		fmt.Fprintln(a.Stdout, "next: clawtool autopilot list --status proposed")
	} else if len(res.Ideas) > 0 {
		fmt.Fprintln(a.Stdout)
		fmt.Fprintln(a.Stdout, "next: clawtool ideate --apply  # push to autopilot at status=proposed")
	}
}

// orderedSourceNames returns the keys of m in a stable order (the
// canonical default-source order, then any extras alphabetised).
func orderedSourceNames(m map[string]int) []string {
	canonical := []string{"adr_questions", "todos", "ci_failures", "manifest_drift", "bench_regression", "deps_outdated"}
	out := make([]string, 0, len(m))
	seen := map[string]struct{}{}
	for _, name := range canonical {
		if _, ok := m[name]; ok {
			out = append(out, name)
			seen[name] = struct{}{}
		}
	}
	for name := range m {
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, name)
	}
	// alphabetise the tail for determinism
	for i := len(canonical); i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// defaultIdeatorSources returns the canonical bundle of sources
// every `clawtool ideate` invocation considers. Delegates to
// `sources.DefaultSources` so the CLI edge and the MCP `IdeateRun`
// edge share the same list — drift between the two used to silently
// break parity (operator hit this when MCP IdeateRun showed 8
// sources while the CLI showed 12).
func defaultIdeatorSources() []ideator.IdeaSource {
	return sources.DefaultSources()
}
