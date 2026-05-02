// Package sources — deadcode_hits source.
//
// Calls `deadcode -test ./...` from repoRoot and converts each
// reported unreachable function into an Idea. The deadcode tool
// (golang.org/x/tools/cmd/deadcode) does Rapid Type Analysis on
// the program's call graph; anything it lists is statically
// unreachable from any main + every test binary.
//
// Cheap-on-fail: a missing `deadcode` binary on PATH or any
// non-zero exit from the tool returns `[]Idea{}, nil` with a
// silent no-op — the orchestrator's per-source warn pipeline only
// surfaces hard errors, and an unreachable-function survey is a
// nice-to-have, not a release gate.
//
// Filters (applied before Idea emission, so the orchestrator's
// dedupe / top-K only sees survivors):
//
//   - `_test.go` — test helpers go through deadcode-via-`-test`,
//     but a test fixture being unreachable is rarely a real
//     finding; skip to keep the queue actionable.
//   - `*_gen.go` — generator-owned code; the generator decides
//     when to delete, not the operator.
//   - `internal/mcpgen/` — template scaffolds: the body of an
//     adapter file is emitted as user-code, deadcode flags it
//     because nothing in this repo calls it.
//   - `internal/checkpoint/` — has uncalled-yet-deliberate
//     helpers (the `wip!` autocommit/autosquash branch in
//     resolve.go landed pre-wired; the wiring lives in a feature
//     flag still being staged). Filter rather than re-flag every
//     run.
package sources

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"github.com/cogitave/clawtool/internal/ideator"
)

// DeadcodeHits implements IdeaSource by shelling out to the
// `deadcode` tool and converting each finding into an Idea.
type DeadcodeHits struct {
	// Binary lets tests inject a stub script; defaults to
	// "deadcode". The lookup falls back to whatever `exec.LookPath`
	// finds on $PATH, which on Go developer hosts typically
	// resolves into $GOPATH/bin.
	Binary string
	// Args overrides the argv passed to the binary; defaults to
	// `-test ./...`. Tests that pin a fixture file feed
	// `-test ./fixturepkg` etc.
	Args []string
	// SkipPathFragments are forward-slash substrings; any finding
	// whose file path contains one (case-insensitive) is dropped.
	// Defaults documented at NewDeadcodeHits.
	SkipPathFragments []string
}

// NewDeadcodeHits returns a ready-to-use deadcode-hits miner with
// the canonical filter list (see package doc).
func NewDeadcodeHits() *DeadcodeHits {
	return &DeadcodeHits{
		Binary: "deadcode",
		Args:   []string{"-test", "./..."},
		SkipPathFragments: []string{
			"internal/mcpgen/",
			"internal/checkpoint/",
		},
	}
}

// Name returns the canonical source name.
func (DeadcodeHits) Name() string { return "deadcode_hits" }

// Scan runs `deadcode -test ./...` from repoRoot and emits one
// Idea per surviving finding. Missing binary / non-zero exit /
// unparseable output → empty slice + nil error (cheap-on-fail).
func (d DeadcodeHits) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	bin := d.Binary
	if bin == "" {
		bin = "deadcode"
	}
	if _, err := exec.LookPath(bin); err != nil {
		// `deadcode` not on PATH — quiet no-op. Operator can
		// install via `go install golang.org/x/tools/cmd/deadcode@latest`
		// when they want this signal.
		return []ideator.Idea{}, nil
	}
	args := d.Args
	if len(args) == 0 {
		args = []string{"-test", "./..."}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// Build failure / package-load error / context cancel —
		// silent no-op so a transient `go list` blip doesn't
		// poison the orchestrator pass.
		return []ideator.Idea{}, nil
	}
	return parseDeadcodeOutput(string(out), d.SkipPathFragments), nil
}

// parseDeadcodeOutput turns the textual report into Ideas. Each
// line follows
//
//	path/to/file.go:line:col: unreachable func: <name>
//
// (the colon after `func` is the upstream tool's actual format —
// the spec example drops it for brevity). Lines that don't match
// the prefix are silently ignored.
func parseDeadcodeOutput(stdout string, skipFragments []string) []ideator.Idea {
	var ideas []ideator.Idea
	scan := bufio.NewScanner(strings.NewReader(stdout))
	scan.Buffer(make([]byte, 0, 4096), 1<<20)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		idea, ok := parseDeadcodeLine(line, skipFragments)
		if !ok {
			continue
		}
		ideas = append(ideas, idea)
	}
	return ideas
}

// parseDeadcodeLine extracts (path, line) + the function name from
// one report line and applies the path-fragment filters. Returns
// the second tuple element false to mean "skip this line"; the
// caller doesn't need to distinguish "didn't match" from "matched
// but filtered".
func parseDeadcodeLine(line string, skipFragments []string) (ideator.Idea, bool) {
	// `unreachable func` is the upstream marker, with or without
	// a trailing colon. Tolerate both so a future tool tweak
	// doesn't silently drop every finding.
	idx := strings.Index(line, "unreachable func")
	if idx < 0 {
		return ideator.Idea{}, false
	}
	location := strings.TrimSpace(line[:idx])
	location = strings.TrimSuffix(location, ":")
	rest := strings.TrimSpace(line[idx+len("unreachable func"):])
	rest = strings.TrimPrefix(rest, ":")
	name := strings.TrimSpace(rest)
	if name == "" {
		return ideator.Idea{}, false
	}
	// Location is `path:line:col`; we want `path:line` for
	// evidence and `path` for the filter probe.
	path, lineNo := splitDeadcodeLocation(location)
	if path == "" {
		return ideator.Idea{}, false
	}
	if isDeadcodeSkipped(path, skipFragments) {
		return ideator.Idea{}, false
	}
	evidence := path
	if lineNo != "" {
		evidence = path + ":" + lineNo
	}
	hash := sha1.Sum([]byte(path + "|" + name))
	return ideator.Idea{
		Title:             "unreachable: " + name,
		Summary:           fmt.Sprintf("Function %s in %s is statically unreachable per `deadcode -test ./...`. Decide whether the caller is pending wiring or the function is dead, then either wire it up or delete.", name, path),
		Evidence:          evidence,
		SuggestedPriority: 3,
		SuggestedPrompt:   "Investigate: is this dead, or is the caller pending wiring? Either delete or wire up.",
		DedupeKey:         "deadcode_hits:" + hex.EncodeToString(hash[:]),
	}, true
}

// splitDeadcodeLocation breaks `path/to/file.go:42:7` into
// (`path/to/file.go`, `42`). Anything that doesn't have at least
// one colon returns ("", "") and lets the caller skip the line.
// Windows paths (`C:\...`) are not produced by the deadcode tool
// — it always emits forward-slash module-relative paths — so the
// naive split is safe here.
func splitDeadcodeLocation(loc string) (string, string) {
	parts := strings.SplitN(loc, ":", 3)
	switch len(parts) {
	case 0, 1:
		return "", ""
	case 2:
		return parts[0], parts[1]
	default:
		return parts[0], parts[1]
	}
}

// isDeadcodeSkipped reports whether the finding's path should be
// dropped before Idea emission. Three filter classes:
//
//  1. `_test.go` suffix — fixtures + helpers re-surfaced by
//     `-test` analysis are rarely actionable.
//  2. `*_gen.go` suffix — generator-owned code.
//  3. Path-fragment matches — `internal/mcpgen/`,
//     `internal/checkpoint/`, plus whatever the operator wires
//     into the slice.
//
// Path comparison is case-insensitive on the fragments to keep
// the same exclusion list working on Windows / Linux / macOS.
func isDeadcodeSkipped(path string, skipFragments []string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	if strings.HasSuffix(path, "_gen.go") {
		return true
	}
	low := strings.ToLower(path)
	for _, frag := range skipFragments {
		if frag == "" {
			continue
		}
		if strings.Contains(low, strings.ToLower(frag)) {
			return true
		}
	}
	return false
}
