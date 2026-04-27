// Package lint — auto-lint guardrails after Edit/Write (ADR-014 T2,
// design from the 2026-04-26 multi-CLI fan-out).
//
// One Runner exposes a single Lint(ctx, path) method that picks the
// right adapter by file extension, shells out to the upstream linter,
// parses its JSON output, and returns structured findings. Edit /
// Write call the runner immediately after a successful atomic write
// so findings ride back in the same response — agents self-correct
// in the next turn without an async queue.
//
// Per ADR-007: every adapter wraps a maintained linter (golangci-lint,
// eslint, ruff). Adding a language is one new file, zero changes to
// the runner contract.
package lint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Finding is one issue the linter reported. Same shape across every
// language so callers never branch on the linter that produced it.
type Finding struct {
	LineNumber int    `json:"line_number"`
	Column     int    `json:"column"`
	Severity   string `json:"severity"` // "error" | "warning" | "info"
	Tool       string `json:"tool"`     // golangci-lint | eslint | ruff
	Message    string `json:"message"`
}

// Runner walks a single file path through the language adapter that
// matches its extension. Implementations must be safe to call
// concurrently from many Edit/Write invocations.
type Runner interface {
	Lint(ctx context.Context, path string) ([]Finding, error)
}

// adapter is the per-language driver. Each one shells out, parses
// JSON, and returns findings.
type adapter struct {
	tool       string                     // human name, lands in Finding.Tool
	binary     string                     // executable on PATH (e.g. "golangci-lint")
	args       func(path string) []string // argv excluding `binary`
	parse      func(out []byte) ([]Finding, error)
	exitOnFind bool // when true, exit code !=0 just means "found issues" (not an error)
}

// runner is the default Runner. langExt resolves a file extension to
// the right adapter.
type runner struct {
	byExt map[string]*adapter
}

// New returns a Runner pre-wired with the three v0.14 adapters
// (Go / JS-TS / Python). Adapters whose binary is missing on PATH
// silently no-op for that language — the runner doesn't crash a
// normal Edit when the operator hasn't installed every linter.
func New() Runner {
	r := &runner{byExt: map[string]*adapter{}}
	for _, ext := range []string{".go"} {
		r.byExt[ext] = adapterGolangciLint()
	}
	for _, ext := range []string{".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"} {
		r.byExt[ext] = adapterESLint()
	}
	for _, ext := range []string{".py"} {
		r.byExt[ext] = adapterRuff()
	}
	return r
}

// Lint dispatches to the adapter for path's extension. Returns nil
// findings + nil error for unsupported languages or when the linter
// binary isn't on PATH (graceful skip).
func (r *runner) Lint(ctx context.Context, path string) ([]Finding, error) {
	ext := strings.ToLower(filepath.Ext(path))
	a, ok := r.byExt[ext]
	if !ok {
		return nil, nil
	}
	if _, err := exec.LookPath(a.binary); err != nil {
		// Linter not installed; skip silently. Operators who want to
		// enforce linter presence can verify via `clawtool doctor`.
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, a.binary, a.args(path)...)
	out, runErr := cmd.CombinedOutput()
	// Some linters exit non-zero on findings; that's not a runner
	// error. We only bail when the binary genuinely failed (couldn't
	// parse arg, etc.) which JSON parsing surfaces as a parse error.
	findings, parseErr := a.parse(out)
	if parseErr != nil {
		// Build a clear error context: the runner's exit code +
		// the parse failure together explain what went wrong.
		return nil, fmt.Errorf("%s: parse %s output: %w (run-err=%v)", a.tool, a.binary, parseErr, runErr)
	}
	for i := range findings {
		findings[i].Tool = a.tool
	}
	return findings, nil
}

// ── adapters ───────────────────────────────────────────────────────

// adapterGolangciLint wraps `golangci-lint run --out-format json <path>`.
func adapterGolangciLint() *adapter {
	return &adapter{
		tool:   "golangci-lint",
		binary: "golangci-lint",
		args:   func(p string) []string { return []string{"run", "--out-format", "json", p} },
		parse: func(out []byte) ([]Finding, error) {
			// golangci-lint's JSON shape:
			// {"Issues":[{"FromLinter":"...","Text":"...","Severity":"warning",
			//             "Pos":{"Filename":"x.go","Line":5,"Column":2}}]}
			var blob struct {
				Issues []struct {
					Text     string `json:"Text"`
					Severity string `json:"Severity"`
					Pos      struct {
						Line   int `json:"Line"`
						Column int `json:"Column"`
					} `json:"Pos"`
				} `json:"Issues"`
			}
			if len(out) == 0 {
				return nil, nil
			}
			if err := json.Unmarshal(out, &blob); err != nil {
				return nil, err
			}
			findings := make([]Finding, 0, len(blob.Issues))
			for _, iss := range blob.Issues {
				sev := iss.Severity
				if sev == "" {
					sev = "warning"
				}
				findings = append(findings, Finding{
					LineNumber: iss.Pos.Line,
					Column:     iss.Pos.Column,
					Severity:   sev,
					Message:    iss.Text,
				})
			}
			return findings, nil
		},
	}
}

// adapterESLint wraps `eslint --format json <path>`.
func adapterESLint() *adapter {
	return &adapter{
		tool:   "eslint",
		binary: "eslint",
		args:   func(p string) []string { return []string{"--format", "json", p} },
		parse: func(out []byte) ([]Finding, error) {
			// ESLint JSON: array of file-result objects, each with messages[].
			// [{"filePath":"x.js","messages":[{"line":3,"column":1,"severity":2,"message":"..."}]}]
			var arr []struct {
				Messages []struct {
					Line     int    `json:"line"`
					Column   int    `json:"column"`
					Severity int    `json:"severity"` // 1=warn, 2=error
					Message  string `json:"message"`
				} `json:"messages"`
			}
			if len(out) == 0 {
				return nil, nil
			}
			if err := json.Unmarshal(out, &arr); err != nil {
				return nil, err
			}
			var findings []Finding
			for _, file := range arr {
				for _, m := range file.Messages {
					sev := "warning"
					if m.Severity >= 2 {
						sev = "error"
					}
					findings = append(findings, Finding{
						LineNumber: m.Line,
						Column:     m.Column,
						Severity:   sev,
						Message:    m.Message,
					})
				}
			}
			return findings, nil
		},
	}
}

// adapterRuff wraps `ruff check --output-format json <path>`.
func adapterRuff() *adapter {
	return &adapter{
		tool:   "ruff",
		binary: "ruff",
		// `--format` was renamed to `--output-format` in Ruff 0.5+;
		// the new spelling is accepted on every supported version.
		args: func(p string) []string {
			return []string{"check", "--output-format", "json", p}
		},
		parse: func(out []byte) ([]Finding, error) {
			// Ruff JSON: array of objects with location.row / column.
			// [{"code":"E501","message":"...","location":{"row":3,"column":1},
			//   "fix":{}}]
			var arr []struct {
				Code     string `json:"code"`
				Message  string `json:"message"`
				Location struct {
					Row    int `json:"row"`
					Column int `json:"column"`
				} `json:"location"`
			}
			if len(out) == 0 {
				return nil, nil
			}
			if err := json.Unmarshal(out, &arr); err != nil {
				return nil, err
			}
			findings := make([]Finding, 0, len(arr))
			for _, m := range arr {
				msg := m.Message
				if m.Code != "" {
					msg = m.Code + ": " + msg
				}
				findings = append(findings, Finding{
					LineNumber: m.Location.Row,
					Column:     m.Location.Column,
					Severity:   "warning",
					Message:    msg,
				})
			}
			return findings, nil
		},
	}
}

// noopRunner is what callers get when AutoLint is disabled. Always
// returns no findings, never errors.
type noopRunner struct{}

func (noopRunner) Lint(_ context.Context, _ string) ([]Finding, error) { return nil, nil }

// Disabled returns a Runner that does nothing — used when
// config.AutoLint.Enabled is explicitly false.
func Disabled() Runner { return noopRunner{} }

// IsEnabled is the helper Edit/Write call to read config.AutoLint.
// Default = true (nil pointer means default-on per the config schema).
func IsEnabled(enabledPtr *bool) bool {
	if enabledPtr == nil {
		return true
	}
	return *enabledPtr
}

// ErrUnsupported is reserved for future use; currently Lint returns
// nil/nil for unsupported extensions rather than erroring (graceful
// skip per the spec). Kept exported in case a stricter mode wants it.
var ErrUnsupported = errors.New("lint: unsupported language")
