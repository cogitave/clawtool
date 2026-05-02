// Package ideator — orchestrator. See source.go for the
// IdeaSource interface and Idea wire shape.
//
// Run executes every enabled source in parallel under a shared
// context, dedupes by Idea.DedupeKey (first-write-wins), scores by
// SuggestedPriority, and returns the top-K. RunAndQueue is the same
// pipeline plus a Propose call into autopilot per surviving Idea —
// every queued row lands at StatusProposed.
package ideator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/cogitave/clawtool/internal/autopilot"
)

// Options is the orchestrator entry-point shape.
//
// SourceFilter (when non-empty) restricts execution to a single
// source by name — drives `clawtool ideate --source adr_questions`.
// Empty means "all enabled sources".
//
// TopK caps the returned slice (after dedupe + score). 0 means
// the package default (DefaultTopK).
//
// Sources defaults to DefaultSources(); tests override it to inject
// stubs.
//
// Warn receives one line per cheap-on-fail (missing CLI, missing
// dir, scan error). Defaults to io.Discard when nil — the
// orchestrator never panics on a quiet caller.
type Options struct {
	RepoRoot     string
	SourceFilter string
	TopK         int
	Sources      []IdeaSource
	Warn         io.Writer
}

// DefaultTopK is the cap when Options.TopK is unset.
const DefaultTopK = 10

// RunResult is the wire shape returned by Run / RunAndQueue. Counts
// surface what the orchestrator did to the operator without making
// them re-run with -v: how many ideas each source emitted, how many
// got deduped, how many ended up queued (RunAndQueue only).
type RunResult struct {
	Ideas        []Idea            `json:"ideas"`
	PerSource    map[string]int    `json:"per_source"`
	Deduped      int               `json:"deduped"`
	Added        int               `json:"added"`
	Skipped      int               `json:"skipped"`
	SourceErrors map[string]string `json:"source_errors,omitempty"`
}

// Run walks every (filtered) source in parallel, dedupes, scores,
// and returns the top-K. No side effects on the autopilot queue —
// use RunAndQueue for that. Caller MUST populate opts.Sources; the
// CLI / MCP edge wires the canonical bundle (see
// internal/cli/ideate.go's defaultSources()) so this package stays
// free of an import cycle into internal/ideator/sources.
func Run(ctx context.Context, opts Options) (RunResult, error) {
	if len(opts.Sources) == 0 {
		return RunResult{PerSource: map[string]int{}}, errors.New("ideator: no sources supplied")
	}
	warn := opts.Warn
	if warn == nil {
		warn = io.Discard
	}
	sources := filterSources(opts.Sources, opts.SourceFilter)
	if len(sources) == 0 {
		return RunResult{PerSource: map[string]int{}}, fmt.Errorf("ideator: no sources match filter %q", opts.SourceFilter)
	}

	type batch struct {
		name  string
		ideas []Idea
		err   error
	}
	results := make([]batch, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src IdeaSource) {
			defer wg.Done()
			ideas, err := src.Scan(ctx, opts.RepoRoot)
			results[i] = batch{name: src.Name(), ideas: ideas, err: err}
		}(i, src)
	}
	wg.Wait()

	// Aggregate: per-source counts, dedupe by key (first wins),
	// drop zero-prompt ideas (defensive — a source returning an
	// empty Idea is a bug we surface but don't crash on).
	out := RunResult{
		PerSource:    make(map[string]int, len(results)),
		SourceErrors: map[string]string{},
	}
	seen := make(map[string]struct{})
	var pooled []Idea
	for _, b := range results {
		out.PerSource[b.name] = len(b.ideas)
		if b.err != nil {
			fmt.Fprintf(warn, "ideator: source %s: %v\n", b.name, b.err)
			out.SourceErrors[b.name] = b.err.Error()
		}
		for _, idea := range b.ideas {
			idea = withDefaults(idea, b.name)
			if strings.TrimSpace(idea.SuggestedPrompt) == "" {
				continue
			}
			if idea.DedupeKey != "" {
				if _, dup := seen[idea.DedupeKey]; dup {
					out.Deduped++
					continue
				}
				seen[idea.DedupeKey] = struct{}{}
			}
			pooled = append(pooled, idea)
		}
	}

	sortIdeas(pooled)
	cap := opts.TopK
	if cap <= 0 {
		cap = DefaultTopK
	}
	if len(pooled) > cap {
		pooled = pooled[:cap]
	}
	out.Ideas = pooled
	return out, nil
}

// RunAndQueue runs the orchestrator then writes each surviving
// Idea into the autopilot queue at StatusProposed. q may be nil —
// in which case autopilot.Open() is used (the default per-host
// store). Returns the same RunResult shape with Added/Skipped
// populated.
//
// Skipped counts items the queue rejected as duplicates (DedupeKey
// already lived on a non-terminal proposed/pending/in_progress row).
// That's by design: re-running ideate after operator inaction must
// be idempotent.
func RunAndQueue(ctx context.Context, opts Options, q *autopilot.Queue) (RunResult, error) {
	res, err := Run(ctx, opts)
	if err != nil {
		return res, err
	}
	if q == nil {
		q = autopilot.Open()
	}
	warn := opts.Warn
	if warn == nil {
		warn = io.Discard
	}
	for _, idea := range res.Ideas {
		_, perr := q.Propose(autopilot.ProposeInput{
			Prompt:    idea.SuggestedPrompt,
			Priority:  idea.SuggestedPriority,
			Note:      idea.Summary,
			Source:    idea.SourceName,
			Evidence:  idea.Evidence,
			DedupeKey: idea.DedupeKey,
		})
		switch {
		case errors.Is(perr, autopilot.ErrDuplicateProposal):
			res.Skipped++
		case perr != nil:
			fmt.Fprintf(warn, "ideator: propose %q: %v\n", idea.Title, perr)
		default:
			res.Added++
		}
	}
	return res, nil
}

// filterSources returns the sources matching name (or all when
// name is empty / "all").
func filterSources(all []IdeaSource, name string) []IdeaSource {
	name = strings.TrimSpace(name)
	if name == "" || name == "all" {
		return all
	}
	out := make([]IdeaSource, 0, 1)
	for _, s := range all {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// withDefaults fills in SourceName / Title / SuggestedPriority
// defaults so source authors don't have to re-state the obvious in
// every Idea they emit.
func withDefaults(in Idea, sourceName string) Idea {
	if in.SourceName == "" {
		in.SourceName = sourceName
	}
	if in.Title == "" {
		// Use the prompt's first line as the title. Most prompts
		// open with an imperative ("Investigate ...", "Wire ..."),
		// which is exactly what the list view wants.
		title := strings.SplitN(in.SuggestedPrompt, "\n", 2)[0]
		if len(title) > 80 {
			title = title[:77] + "..."
		}
		in.Title = title
	}
	return in
}

// sortIdeas orders by SuggestedPriority desc, then by SourceName /
// DedupeKey lexicographically for determinism (stable across runs
// of the same repo state, so the operator sees the same ranking
// twice).
func sortIdeas(ideas []Idea) {
	sort.SliceStable(ideas, func(i, j int) bool {
		a, b := ideas[i], ideas[j]
		if a.SuggestedPriority != b.SuggestedPriority {
			return a.SuggestedPriority > b.SuggestedPriority
		}
		if a.SourceName != b.SourceName {
			return a.SourceName < b.SourceName
		}
		return a.DedupeKey < b.DedupeKey
	})
}
