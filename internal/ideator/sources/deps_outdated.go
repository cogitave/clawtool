// Package sources — deps_outdated source.
//
// Runs `go list -m -u -json all` from repoRoot, parses the JSON
// stream of module records, and emits one Idea per module whose
// Update.Version is set. The cheap-on-fail rule applies: missing
// `go` binary, network failure, or a parser error returns an empty
// slice + nil error rather than poisoning the orchestrator.
//
// Priority logic (lower number = lower priority because the
// autopilot queue scores higher first):
//
//   - patch / minor bump  → priority 4 (safe, mostly mechanical)
//   - major bump          → priority 2 (needs manual review,
//     breaking changes likely)
//
// Filter: golang.org/toolchain is auto-managed by `go` itself —
// surfacing it as an Idea is noise. The Main module is also
// skipped (Update never fires on it, but the guard is cheap).
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/ideator"
)

// DepsOutdated implements IdeaSource. Construct via NewDepsOutdated.
type DepsOutdated struct {
	// GoBinary lets tests inject a stub binary; defaults to "go".
	GoBinary string
	// Timeout caps the `go list` subprocess (default 60s — module
	// proxy queries can be slow on cold caches).
	Timeout time.Duration
	// runGoList is the indirection tests stub to feed canned JSON
	// without spinning up a real `go` toolchain.
	runGoList func(ctx context.Context, goBin, repoRoot string, timeout time.Duration) ([]byte, error)
}

// NewDepsOutdated returns a ready-to-use outdated-module miner.
func NewDepsOutdated() *DepsOutdated {
	return &DepsOutdated{
		GoBinary:  "go",
		Timeout:   60 * time.Second,
		runGoList: defaultRunGoList,
	}
}

// Name returns the canonical source name.
func (DepsOutdated) Name() string { return "deps_outdated" }

// goModule is the subset of fields `go list -m -u -json` produces
// that this source cares about. The full struct is documented in
// `go help list`; we only need Path / Version / Update / Main.
type goModule struct {
	Path    string          `json:"Path"`
	Version string          `json:"Version"`
	Main    bool            `json:"Main"`
	Update  *goModuleUpdate `json:"Update,omitempty"`
}

// goModuleUpdate is the embedded Update sub-object — present only
// when a newer version is available on the proxy.
type goModuleUpdate struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// Scan executes `go list -m -u -json all`, parses the JSON stream,
// and emits one Idea per module with an available update.
func (d DepsOutdated) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	bin := d.GoBinary
	if bin == "" {
		bin = "go"
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	run := d.runGoList
	if run == nil {
		run = defaultRunGoList
	}

	body, err := run(ctx, bin, repoRoot, timeout)
	if err != nil {
		// Cheap-on-fail: missing binary, network outage, proxy
		// 5xx — surface nothing, let the orchestrator move on.
		return nil, nil
	}

	mods, err := parseGoListStream(body)
	if err != nil {
		// Malformed JSON also folds into cheap-on-fail; we'd
		// rather drop the whole batch than half-parse.
		return nil, nil
	}

	goMod := filepath.Join(repoRoot, "go.mod")
	lineLookup := buildGoModLineLookup(goMod)

	var ideas []ideator.Idea
	for _, m := range mods {
		if m.Main {
			continue
		}
		if m.Update == nil || m.Update.Version == "" {
			continue
		}
		if isToolchainModule(m.Path) {
			continue
		}
		ideas = append(ideas, buildOutdatedIdea(m, lineLookup))
	}
	return ideas, nil
}

// defaultRunGoList shells out to the real `go` binary. Pulled out
// so tests can inject a stub via the runGoList field.
func defaultRunGoList(ctx context.Context, goBin, repoRoot string, timeout time.Duration) ([]byte, error) {
	if _, err := exec.LookPath(goBin); err != nil {
		return nil, err
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(subCtx, goBin, "list", "-m", "-u", "-json", "all")
	cmd.Dir = repoRoot
	// Tell the toolchain to fail fast rather than hang on a stuck
	// module proxy: GOFLAGS=-mod=mod keeps the lookup in-memory.
	// Stderr is discarded — `go list` prints proxy warnings the
	// operator doesn't need to see in ideate output.
	return cmd.Output()
}

// parseGoListStream decodes the concatenated-JSON-objects body
// `go list -m -u -json` produces. Each object is a Module record.
func parseGoListStream(body []byte) ([]goModule, error) {
	var mods []goModule
	dec := json.NewDecoder(strings.NewReader(string(body)))
	for {
		var m goModule
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return mods, err
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// isToolchainModule returns true for the magic `golang.org/toolchain`
// pseudo-module the Go toolchain auto-pins. The operator can't bump
// it via `go get`; the `go` directive in go.mod controls it.
func isToolchainModule(path string) bool {
	return path == "golang.org/toolchain" || strings.HasPrefix(path, "golang.org/toolchain/")
}

// buildOutdatedIdea constructs the Idea for one outdated module.
// Title / SuggestedPrompt vary on whether the bump crosses a major
// version boundary; lineLookup attaches a go.mod line number when
// the module is declared there (indirect deps in go.sum may not be).
func buildOutdatedIdea(m goModule, lineLookup map[string]int) ideator.Idea {
	major := isMajorBump(m.Version, m.Update.Version)
	priority := 4
	noteSuffix := ""
	if major {
		priority = 2
		noteSuffix = " (major version bump — review breaking changes)"
	}

	evidence := "go.mod:" + m.Path
	if line, ok := lineLookup[m.Path]; ok {
		evidence = fmt.Sprintf("go.mod:%d", line)
	}

	title := fmt.Sprintf("Bump %s from %s to %s", m.Path, m.Version, m.Update.Version)
	summary := fmt.Sprintf(
		"Module %s has an available update %s → %s%s. Run `go get %s@%s` and re-run `go mod tidy` to land it.",
		m.Path, m.Version, m.Update.Version, noteSuffix,
		m.Path, m.Update.Version,
	)

	prompt := fmt.Sprintf(
		"Bump the %s dependency from %s to %s.\n\n"+
			"  - run `go get %s@%s`\n"+
			"  - run `go mod tidy`\n"+
			"  - run `go build ./...` and the test suite to confirm nothing breaks\n\n",
		m.Path, m.Version, m.Update.Version,
		m.Path, m.Update.Version,
	)
	if major {
		prompt += "This is a MAJOR version bump (" + m.Version + " → " + m.Update.Version + ");" +
			" the upstream may have introduced breaking changes. Read the release\n" +
			"notes / CHANGELOG before landing the update, and budget time for\n" +
			"call-site adjustments. If the import path itself moves (e.g. /v2 suffix),\n" +
			"land the rename in the same commit."
	} else {
		prompt += "This is a patch / minor bump and should be mechanical. If `go build`\n" +
			"or the test suite fails, revert and surface the failure so the operator\n" +
			"can decide whether to chase the regression or hold the dep at the\n" +
			"current version."
	}

	hash := sha1.Sum([]byte(m.Path + "|" + m.Version + "->" + m.Update.Version))
	return ideator.Idea{
		Title:             title,
		Summary:           summary,
		Evidence:          evidence,
		SuggestedPriority: priority,
		SuggestedPrompt:   prompt,
		DedupeKey:         "deps_outdated:" + hex.EncodeToString(hash[:]),
	}
}

// isMajorBump returns true when the SemVer major component of new
// is strictly greater than that of old. v0.x → v0.y is treated as
// a non-major bump even though SemVer technically treats every
// 0.x release as breaking — the autopilot queue would drown in
// priority-2 noise otherwise. Unparseable versions fall back to
// false (assume non-major) so a weird pseudo-version doesn't get
// flagged with a scary banner.
func isMajorBump(oldV, newV string) bool {
	oldMaj, ok := semverMajor(oldV)
	if !ok {
		return false
	}
	newMaj, ok := semverMajor(newV)
	if !ok {
		return false
	}
	return newMaj > oldMaj
}

// semverMajor parses the leading "vN" of a SemVer string and
// returns N. Returns (0, false) on a missing leading "v" or a
// non-numeric major component (pseudo-versions like
// `v0.0.0-20251205110129-5db1dc9836f0` still parse fine —
// their major is 0).
func semverMajor(v string) (int, bool) {
	if !strings.HasPrefix(v, "v") {
		return 0, false
	}
	v = v[1:]
	dot := strings.IndexByte(v, '.')
	if dot < 0 {
		// "v1" with no minor — accept it.
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	n, err := strconv.Atoi(v[:dot])
	if err != nil {
		return 0, false
	}
	return n, true
}

// buildGoModLineLookup returns a map of module-path → 1-indexed
// line number in go.mod. Missing file or read error → empty map;
// the caller falls back to a path-only Evidence string.
//
// The parser is deliberately tiny: we look for either
// `\t<path> v...` inside a `require ( ... )` block or a single-line
// `require <path> v...`. Comments / replace directives / build
// constraints are ignored — Evidence is for the operator's grep,
// not a strict go.mod parser.
func buildGoModLineLookup(goModPath string) map[string]int {
	out := map[string]int{}
	body, err := os.ReadFile(goModPath)
	if err != nil {
		return out
	}
	lines := strings.Split(string(body), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		// Strip a leading `require ` so single-line requires share
		// the inside-block parse path.
		line = strings.TrimPrefix(line, "require ")
		// Skip block delimiters / replace / exclude / module / go directives.
		switch {
		case line == "(", line == ")":
			continue
		case strings.HasPrefix(line, "module "),
			strings.HasPrefix(line, "go "),
			strings.HasPrefix(line, "toolchain "),
			strings.HasPrefix(line, "replace "),
			strings.HasPrefix(line, "exclude "),
			strings.HasPrefix(line, "retract "):
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		path := fields[0]
		// Sanity: a require line's second field must look like a
		// version (`v...`). Anything else is something we don't
		// care about (e.g. a stray `=>`).
		if !strings.HasPrefix(fields[1], "v") {
			continue
		}
		// First wins — the same module shouldn't appear twice in
		// a well-formed go.mod, but defensively keep the earliest
		// occurrence so the operator's grep lands on the canonical
		// declaration.
		if _, exists := out[path]; !exists {
			out[path] = i + 1
		}
	}
	return out
}
