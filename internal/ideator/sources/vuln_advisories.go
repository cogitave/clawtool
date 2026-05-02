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
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	// WorkflowGoVersionFile points to a YAML file whose
	// `GO_VERSION:` line is treated as the authoritative toolchain
	// pin for stdlib advisories. When set + readable, stdlib
	// findings whose `fixed_version` is <= the workflow pin are
	// dropped — the published binary already runs the fix even if
	// the developer's local Go install is older. Defaults to
	// `.github/workflows/ci.yml`. Set to "" to disable.
	WorkflowGoVersionFile string
	// CachePath stores the most-recent raw govulncheck output. When
	// the cache is fresher than CacheTTL AND the cache key (go.sum
	// hash + govulncheck version) matches, Scan skips the subprocess
	// entirely. Default `$TMPDIR/clawtool-govulncheck-cache.json`.
	// Set to "" to disable caching.
	CachePath string
	// CacheTTL is how long the on-disk govulncheck output stays
	// authoritative. Default 12h: the Go vulndb publishes daily, so
	// 12h gives at most one stale call between db refreshes; on a
	// slow filesystem (WSL2 /mnt/c) the win is enormous (~16s → ~0.1s).
	CacheTTL time.Duration
	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
}

// NewVulnAdvisories returns a ready-to-use vuln_advisories miner.
func NewVulnAdvisories() *VulnAdvisories {
	return &VulnAdvisories{
		Binary:                "govulncheck",
		Timeout:               90 * time.Second,
		Pattern:               "./...",
		WorkflowGoVersionFile: ".github/workflows/ci.yml",
		CachePath:             filepath.Join(os.TempDir(), "clawtool-govulncheck-cache.json"),
		CacheTTL:              12 * time.Hour,
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
//
// When a cache hit on the local go.sum hash is fresh enough
// (CacheTTL), the subprocess is skipped and the cached output is
// re-parsed — turning a 16s scan into ~50ms.
func (v VulnAdvisories) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	bin := v.Binary
	if bin == "" {
		bin = "govulncheck"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return nil, nil
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	cacheKey := cacheKeyFor(repoRoot, bin)
	if body, ok := v.readCache(cacheKey, now()); ok {
		advisories, findings := parseGovulncheckStream(strings.NewReader(body))
		workflowPin := v.readWorkflowGoVersion(repoRoot)
		return buildVulnIdeas(advisories, findings, workflowPin), nil
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
	out, err := cmd.Output()
	if err != nil {
		// Govulncheck exits non-zero on findings — that's normal,
		// out still has the JSON body. Only treat as error when
		// out is empty (real fault: missing module, network).
		if len(out) == 0 {
			return nil, nil
		}
	}
	advisories, findings := parseGovulncheckStream(strings.NewReader(string(out)))
	v.writeCache(cacheKey, string(out), now())
	workflowPin := v.readWorkflowGoVersion(repoRoot)
	return buildVulnIdeas(advisories, findings, workflowPin), nil
}

// vulnCacheEntry is the on-disk shape. Key is the cacheKeyFor
// fingerprint (govulncheck binary identity + go.sum hash); when
// either changes the cache is invalidated.
type vulnCacheEntry struct {
	Key       string `json:"key"`
	WrittenAt string `json:"written_at"`
	Body      string `json:"body"`
}

// readCache returns the cached govulncheck JSON body when:
//   - CachePath is set + readable,
//   - the file's age is < CacheTTL,
//   - the entry's Key matches the current cacheKey.
//
// Otherwise returns "", false (caller falls through to subprocess).
func (v VulnAdvisories) readCache(cacheKey string, now time.Time) (string, bool) {
	if v.CachePath == "" {
		return "", false
	}
	body, err := os.ReadFile(v.CachePath)
	if err != nil {
		return "", false
	}
	var entry vulnCacheEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return "", false
	}
	if entry.Key != cacheKey {
		return "", false
	}
	written, err := time.Parse(time.RFC3339, entry.WrittenAt)
	if err != nil {
		return "", false
	}
	ttl := v.CacheTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	if now.Sub(written) > ttl {
		return "", false
	}
	return entry.Body, true
}

// writeCache persists the latest govulncheck output. Best-effort —
// disk full, read-only fs, etc. don't fail the scan; we just lose
// the cache hit on the next call.
func (v VulnAdvisories) writeCache(cacheKey, body string, now time.Time) {
	if v.CachePath == "" {
		return
	}
	entry := vulnCacheEntry{
		Key:       cacheKey,
		WrittenAt: now.UTC().Format(time.RFC3339),
		Body:      body,
	}
	enc, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(v.CachePath, enc, 0o600)
}

// cacheKeyFor fingerprints the inputs that should invalidate the
// cache: the govulncheck binary's resolved path (so a govulncheck
// upgrade busts) and the go.sum content hash (so a dep bump busts).
// Empty key means "don't cache" (binary not found / go.sum missing).
func cacheKeyFor(repoRoot, bin string) string {
	binPath, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	gosum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		// Tolerate missing go.sum — still key on the binary path.
		return "bin=" + binPath
	}
	sum := sha1Hex(gosum)
	return "bin=" + binPath + " gosum=" + sum
}

// sha1Hex hashes b and returns the hex digest.
func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

// readWorkflowGoVersion returns the parsed Go version from the
// configured workflow file (e.g. "1.26.2"), or empty when the file
// is missing / unset / unparsable. Empty string disables the filter.
func (v VulnAdvisories) readWorkflowGoVersion(repoRoot string) string {
	path := v.WorkflowGoVersionFile
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		// Match `  GO_VERSION: "1.26.2"` (any whitespace, optional
		// quotes). Workflow files canonicalise to this shape.
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "GO_VERSION:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(trim, "GO_VERSION:"))
		val = strings.Trim(val, `"' `)
		if val == "" {
			continue
		}
		return val
	}
	return ""
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
//
// workflowGoPin (e.g. "1.26.2") is the authoritative toolchain
// version baked into CI; when set, stdlib findings whose
// `fixed_version` is <= workflowGoPin are dropped — the published
// binary already runs the fix even if the developer's local Go is
// older. Empty string disables the filter.
func buildVulnIdeas(advisories map[string]vulnOSV, findings []vulnFinding, workflowGoPin string) []ideator.Idea {
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
		if module == "stdlib" && workflowGoPin != "" && stdlibFixedByPin(f.FixedVersion, workflowGoPin) {
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

// stdlibFixedByPin returns true when the workflow Go pin already
// covers the advisory's fixed version. Both arguments use Go's
// standard "X.Y" / "X.Y.Z" / "vX.Y.Z" shape; we strip the leading
// "v" and "go" prefixes and compare element-by-element. False
// (don't drop) on any parse error so we fail open.
func stdlibFixedByPin(fixed, pin string) bool {
	a := parseGoVersion(fixed)
	b := parseGoVersion(pin)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for i := 0; i < 3; i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if bv > av {
			return true
		}
		if bv < av {
			return false
		}
	}
	return true // equal counts as fixed
}

// parseGoVersion turns "1.26.2" / "v1.26.2" / "go1.26" into
// []int{1,26,2}. Returns nil on parse failure.
func parseGoVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "go")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		// Drop pre-release suffix: "1.26.0-rc1" → "1.26.0".
		if idx := strings.IndexAny(p, "-+"); idx >= 0 {
			p = p[:idx]
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
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
