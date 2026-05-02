// Package sources — vuln_advisories source.
//
// Runs `govulncheck -json ./...` and emits one Idea per
// (osv, module) finding so the operator decides whether to bump
// the affected module / stdlib pin. Findings are joined against
// the in-stream `osv` advisory blocks so each Idea carries the
// CVE alias + summary, not just the GO-YYYY-NNNN id.
//
// Missing govulncheck binary → silent no-op (cheap-on-fail, same
// shape as ci_failures / deps_outdated).
//
// Dedupe key is `vuln_advisories:<osv-id>:<module>` — bumping the
// module across multiple `finding` blocks (one per call site)
// collapses to one Idea, so 12 stdlib finds turn into 1 actionable
// "bump Go toolchain" item, not 12 noise rows.
package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/ideator"
)

// VulnAdvisories implements IdeaSource for govulncheck output.
type VulnAdvisories struct {
	// Binary lets tests inject a stub `govulncheck`; defaults to
	// the resolved one from $PATH.
	Binary string
	// Timeout caps the subprocess. Govulncheck loads the package
	// graph + queries the vulndb, so 90s is the realistic
	// default — cron-friendly but bounded.
	Timeout time.Duration
	// Pattern is the package pattern to scan; defaults to ./...
	Pattern string
}

// NewVulnAdvisories returns a ready-to-use vuln_advisories miner.
func NewVulnAdvisories() *VulnAdvisories {
	return &VulnAdvisories{
		Binary:  "govulncheck",
		Timeout: 90 * time.Second,
		Pattern: "./...",
	}
}

// Name returns the canonical source name.
func (VulnAdvisories) Name() string { return "vuln_advisories" }

// vulnFinding mirrors the `finding` envelope govulncheck emits.
type vulnFinding struct {
	OSV          string `json:"osv"`
	FixedVersion string `json:"fixed_version"`
	Trace        []struct {
		Module  string `json:"module"`
		Version string `json:"version"`
		Package string `json:"package"`
	} `json:"trace"`
}

// vulnOSV mirrors the `osv` envelope; we only keep the human-readable
// fields the Idea needs.
type vulnOSV struct {
	ID      string   `json:"id"`
	Aliases []string `json:"aliases"`
	Summary string   `json:"summary"`
}

// vulnEnvelope is the top-level discriminated union govulncheck
// streams: each top-level object has exactly one of these keys.
type vulnEnvelope struct {
	OSV     *vulnOSV     `json:"osv,omitempty"`
	Finding *vulnFinding `json:"finding,omitempty"`
}

// Scan calls govulncheck, parses the JSON stream, joins findings
// against advisory metadata, and emits deduped Ideas. Empty +
// nil error on missing binary / non-zero exit / parse failure
// (cheap-on-fail).
func (v VulnAdvisories) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	bin := v.Binary
	if bin == "" {
		bin = "govulncheck"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, nil
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	pattern := v.Pattern
	if pattern == "" {
		pattern = "./..."
	}
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(subCtx, bin, "-json", pattern)
	cmd.Dir = repoRoot
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil
	}
	if err := cmd.Start(); err != nil {
		return nil, nil
	}
	advisories, findings := parseGovulncheckStream(stdout)
	// Drain anything left + reap. Govulncheck exits non-zero when
	// it finds vulns; that's a normal outcome here, not an error.
	_ = cmd.Wait()
	return buildVulnIdeas(advisories, findings), nil
}

// parseGovulncheckStream reads the JSON stream and returns the
// advisory map + findings list. Reader errors / unexpected envelope
// shapes are skipped silently; the caller treats this as cheap-on-
// fail.
func parseGovulncheckStream(r io.Reader) (map[string]vulnOSV, []vulnFinding) {
	dec := json.NewDecoder(r)
	advisories := make(map[string]vulnOSV)
	var findings []vulnFinding
	for {
		var env vulnEnvelope
		if err := dec.Decode(&env); err != nil {
			break
		}
		switch {
		case env.OSV != nil && env.OSV.ID != "":
			advisories[env.OSV.ID] = *env.OSV
		case env.Finding != nil && env.Finding.OSV != "":
			findings = append(findings, *env.Finding)
		}
	}
	return advisories, findings
}

// buildVulnIdeas joins findings to advisories and dedupes
// by (osv, module). Returns ideas in stable order: highest
// priority first, then by osv id ascending.
func buildVulnIdeas(advisories map[string]vulnOSV, findings []vulnFinding) []ideator.Idea {
	type key struct {
		osv    string
		module string
	}
	seen := make(map[key]bool)
	ideas := make([]ideator.Idea, 0, len(findings))
	for _, f := range findings {
		module := primaryModule(f)
		if module == "" {
			continue
		}
		k := key{osv: f.OSV, module: module}
		if seen[k] {
			continue
		}
		seen[k] = true
		osv, ok := advisories[f.OSV]
		summary := ""
		if ok {
			summary = osv.Summary
		}
		title := f.OSV
		if summary != "" {
			title = f.OSV + ": " + summary
		}
		evidence := fmt.Sprintf("govulncheck %s in %s", f.OSV, module)
		alias := ""
		if ok && len(osv.Aliases) > 0 {
			alias = strings.Join(osv.Aliases, ", ")
		}
		prompt := fmt.Sprintf(
			"Address Go vulnerability %s in module %s.\n\n"+
				"  - osv id: %s\n"+
				"  - aliases: %s\n"+
				"  - summary: %s\n"+
				"  - fixed in: %s\n\n"+
				"For stdlib findings, bump the Go toolchain pin in `go.mod` and the workflow `GO_VERSION` env. "+
				"For module findings, run `go get %s@%s && go mod tidy`. "+
				"Verify by re-running `govulncheck ./...` until the OSV id disappears.",
			f.OSV, module, f.OSV, alias, summary, f.FixedVersion, module, f.FixedVersion,
		)
		ideas = append(ideas, ideator.Idea{
			Title:             title,
			Summary:           fmt.Sprintf("%s (in %s, fixed in %s)", summary, module, f.FixedVersion),
			Evidence:          evidence,
			SuggestedPriority: priorityForVuln(module),
			SuggestedPrompt:   prompt,
			DedupeKey:         "vuln_advisories:" + f.OSV + ":" + module,
		})
	}
	sort.SliceStable(ideas, func(i, j int) bool {
		if ideas[i].SuggestedPriority != ideas[j].SuggestedPriority {
			return ideas[i].SuggestedPriority > ideas[j].SuggestedPriority
		}
		return ideas[i].DedupeKey < ideas[j].DedupeKey
	})
	return ideas
}

// primaryModule picks the module the finding is "for". Govulncheck
// emits a trace where the first hop is the affected module + version;
// we use that. Empty string means an unparseable finding (skip).
func primaryModule(f vulnFinding) string {
	if len(f.Trace) == 0 {
		return ""
	}
	return f.Trace[0].Module
}

// priorityForVuln returns the SuggestedPriority. Stdlib vulns get
// priority 8 (one above ci_failures' 7) because they affect every
// build; third-party module vulns get 6 — same severity tier as
// deadcode but below ci_failures since the user can ship a fix
// without waiting on upstream.
func priorityForVuln(module string) int {
	if module == "stdlib" {
		return 8
	}
	return 6
}
