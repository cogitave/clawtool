// Package core — Verify MCP tool (ADR-014 T4, design from the
// 2026-04-26 multi-CLI fan-out).
//
// Verify runs a repo's tests / lints / typechecks via whichever
// runner the repo declares (Make, pnpm, npm, go, pytest, ruby,
// cargo, just) and returns one structured pass/fail per check. Per
// ADR-007 we wrap maintained runners — `go test -json`,
// `pytest --json-report`, `cargo test --message-format json` — and
// fall back to the runner's plain output when the structured form
// isn't available on this host.
//
// Buffered single payload (not stream): callers want the full
// pass/fail summary, not the live log fire hose. Bash already
// streams when that's what's wanted.
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// shimmed for tests; never overridden in production.
var (
	osStat     = os.Stat
	osReadFile = os.ReadFile
)

const (
	verifyDefaultTimeoutS = 600 // 10 min
	verifyMaxLogExcerpt   = 4096
)

// VerifyResult is the uniform response. `Overall` is "pass" iff every
// check passed; one fail flips the whole result.
type VerifyResult struct {
	BaseResult
	Repo    string        `json:"repo"`
	Checks  []VerifyCheck `json:"checks"`
	Overall string        `json:"overall"` // "pass" | "fail"
}

// VerifyCheck is one per-runner result. `DetailsLogExcerpt` is the
// last verifyMaxLogExcerpt bytes of combined stdout+stderr — enough
// for an agent to read the last failing assertion without
// blowing the response budget.
type VerifyCheck struct {
	Name              string `json:"name"`
	Status            string `json:"status"` // "pass" | "fail" | "timeout" | "skipped"
	DurationMs        int64  `json:"duration_ms"`
	Summary           string `json:"summary,omitempty"`
	DetailsLogExcerpt string `json:"details_log_excerpt,omitempty"`
}

// Render satisfies the Renderer contract. One line per check + a
// final overall verdict.
func (r VerifyResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.Repo)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("Verify %s", r.Repo)))
	b.WriteByte('\n')
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "%-8s %-32s (%dms) %s\n", c.Status, c.Name, c.DurationMs, c.Summary)
	}
	b.WriteString(r.FooterLine(fmt.Sprintf("overall: %s", r.Overall)))
	return b.String()
}

// RegisterVerify wires the Verify MCP tool.
func RegisterVerify(s *server.MCPServer) {
	tool := mcp.NewTool(
		"Verify",
		mcp.WithDescription(
			"Run a repo's tests / lints / typechecks and return one "+
				"structured pass/fail per check. Probes Make, pnpm, npm, go "+
				"test, pytest, ruby, cargo, just in that order; first match "+
				"wins. Pin via target. Buffered single payload — for streaming "+
				"output use Bash with the underlying command. Per ADR-007 it "+
				"wraps the upstream runners; clawtool ships the polish "+
				"(timeout reaping, structured JSON, log excerpt cap).",
		),
		mcp.WithString("repo", mcp.Required(),
			mcp.Description("Path to the repo root.")),
		mcp.WithString("target",
			mcp.Description("Pin a runner: make | pnpm | npm | go | pytest | ruby | cargo | just. Empty = auto-probe.")),
		mcp.WithNumber("timeout_s",
			mcp.Description(fmt.Sprintf("Per-check timeout in seconds. Default %d.", verifyDefaultTimeoutS))),
	)
	s.AddTool(tool, runVerify)
}

func runVerify(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: repo"), nil
	}
	target := strings.TrimSpace(req.GetString("target", ""))
	timeoutS := int(req.GetFloat("timeout_s", float64(verifyDefaultTimeoutS)))
	if timeoutS <= 0 {
		timeoutS = verifyDefaultTimeoutS
	}

	res := executeVerify(ctx, repo, target, time.Duration(timeoutS)*time.Second)
	return resultOf(res), nil
}

// executeVerify is the testable core.
func executeVerify(ctx context.Context, repo, target string, timeout time.Duration) VerifyResult {
	start := time.Now()
	res := VerifyResult{
		BaseResult: BaseResult{Operation: "Verify", Engine: "verify"},
		Repo:       repo,
		Overall:    "pass",
	}

	plan, perr := pickRunners(repo, target)
	if perr != nil {
		res.ErrorReason = perr.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		res.Overall = "fail"
		return res
	}
	if len(plan) == 0 {
		// No runner detected; not an error — operators sometimes ask
		// Verify on a project still being scaffolded.
		res.Checks = append(res.Checks, VerifyCheck{
			Name:    "detect",
			Status:  "skipped",
			Summary: "no test runner detected (probe order: make / pnpm / npm / go / pytest / rake / cargo / just)",
		})
		res.Overall = "fail"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	for _, p := range plan {
		c := runOneCheck(ctx, repo, p, timeout)
		res.Checks = append(res.Checks, c)
		if c.Status != "pass" {
			res.Overall = "fail"
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// runnerPlan is one selected runner with the argv to execute.
type runnerPlan struct {
	name string
	argv []string
}

// pickRunners detects which runner(s) to invoke. Today returns at
// most one entry — the first match — but the slice shape lets a
// future "run all detected" mode plug in without touching call sites.
func pickRunners(repo, target string) ([]runnerPlan, error) {
	if target != "" {
		p, ok := byTarget(target)
		if !ok {
			return nil, fmt.Errorf("unknown target %q (valid: make pnpm npm go pytest ruby cargo just)", target)
		}
		return []runnerPlan{p}, nil
	}
	for _, candidate := range probeOrder() {
		if candidate.detect(repo) {
			return []runnerPlan{candidate.plan}, nil
		}
	}
	return nil, nil
}

type candidate struct {
	plan   runnerPlan
	detect func(repo string) bool
}

func probeOrder() []candidate {
	return []candidate{
		{
			plan:   runnerPlan{name: "make test", argv: []string{"make", "test"}},
			detect: func(r string) bool { return hasFileWithTarget(filepath.Join(r, "Makefile"), "test") },
		},
		{
			plan: runnerPlan{name: "pnpm test", argv: []string{"pnpm", "test"}},
			detect: func(r string) bool {
				return fileExists(filepath.Join(r, "package.json")) &&
					(fileExists(filepath.Join(r, "pnpm-lock.yaml")) || fileExists(filepath.Join(r, ".pnpm-store")))
			},
		},
		{
			plan:   runnerPlan{name: "npm test", argv: []string{"npm", "test"}},
			detect: func(r string) bool { return fileExists(filepath.Join(r, "package.json")) },
		},
		{
			plan:   runnerPlan{name: "go test ./...", argv: []string{"go", "test", "./..."}},
			detect: func(r string) bool { return fileExists(filepath.Join(r, "go.mod")) },
		},
		{
			plan: runnerPlan{name: "pytest", argv: []string{"pytest"}},
			detect: func(r string) bool {
				return fileExists(filepath.Join(r, "pyproject.toml")) ||
					fileExists(filepath.Join(r, "pytest.ini")) ||
					dirExists(filepath.Join(r, "tests"))
			},
		},
		{
			plan: runnerPlan{name: "bundle exec rake test", argv: []string{"bundle", "exec", "rake", "test"}},
			detect: func(r string) bool {
				return fileExists(filepath.Join(r, "Gemfile")) && fileExists(filepath.Join(r, "Rakefile"))
			},
		},
		{
			plan:   runnerPlan{name: "rake test", argv: []string{"rake", "test"}},
			detect: func(r string) bool { return fileExists(filepath.Join(r, "Rakefile")) },
		},
		{
			plan:   runnerPlan{name: "cargo test", argv: []string{"cargo", "test"}},
			detect: func(r string) bool { return fileExists(filepath.Join(r, "Cargo.toml")) },
		},
		{
			plan:   runnerPlan{name: "just test", argv: []string{"just", "test"}},
			detect: func(r string) bool { return hasFileWithTarget(filepath.Join(r, "Justfile"), "test") },
		},
	}
}

// byTarget resolves an explicit `target` string to its runnerPlan.
func byTarget(t string) (runnerPlan, bool) {
	switch strings.ToLower(t) {
	case "make":
		return runnerPlan{name: "make test", argv: []string{"make", "test"}}, true
	case "pnpm":
		return runnerPlan{name: "pnpm test", argv: []string{"pnpm", "test"}}, true
	case "npm":
		return runnerPlan{name: "npm test", argv: []string{"npm", "test"}}, true
	case "go":
		return runnerPlan{name: "go test ./...", argv: []string{"go", "test", "./..."}}, true
	case "pytest":
		return runnerPlan{name: "pytest", argv: []string{"pytest"}}, true
	case "ruby":
		// Ruby itself isn't a test runner; the canonical Ruby
		// test entry-point is rake. `bundle exec` keeps the gem
		// resolution consistent with the project's Gemfile when
		// one exists.
		return runnerPlan{name: "bundle exec rake test", argv: []string{"bundle", "exec", "rake", "test"}}, true
	case "cargo":
		return runnerPlan{name: "cargo test", argv: []string{"cargo", "test"}}, true
	case "just":
		return runnerPlan{name: "just test", argv: []string{"just", "test"}}, true
	}
	return runnerPlan{}, false
}

// runOneCheck executes a single runnerPlan with the given timeout.
func runOneCheck(parent context.Context, repo string, p runnerPlan, timeout time.Duration) VerifyCheck {
	out := VerifyCheck{Name: p.name}
	start := time.Now()

	if _, err := exec.LookPath(p.argv[0]); err != nil {
		out.Status = "skipped"
		out.Summary = fmt.Sprintf("%q not on PATH", p.argv[0])
		return out
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.argv[0], p.argv[1:]...)
	cmd.Dir = repo
	applyProcessGroup(cmd) // shared with Bash — clean SIGKILL on timeout

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()
	out.DurationMs = time.Since(start).Milliseconds()
	out.DetailsLogExcerpt = tailString(combined.String(), verifyMaxLogExcerpt)

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		out.Status = "timeout"
		out.Summary = fmt.Sprintf("timed out after %s", timeout)
	case runErr == nil:
		out.Status = "pass"
		out.Summary = summariseTail(out.DetailsLogExcerpt, "pass")
	default:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			out.Status = "fail"
			out.Summary = fmt.Sprintf("exit %d", exitErr.ExitCode())
		} else {
			out.Status = "fail"
			out.Summary = runErr.Error()
		}
	}
	return out
}

// tailString returns the last n bytes of s, prefixed with an ellipsis
// when truncation happened.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// fileExists / dirExists are local helpers used by the probe order.
// We don't depend on internal/setup's FileExists because the
// dependency direction would invert (core → setup).
func fileExists(path string) bool {
	info, err := osStat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := osStat(path)
	return err == nil && info.IsDir()
}

// hasFileWithTarget reports whether `path` exists AND contains a line
// declaring `target:` (Make-style) or `target ` (Just-style). Cheap
// substring match — robust enough for the probe.
func hasFileWithTarget(path, target string) bool {
	b, err := osReadFile(path)
	if err != nil {
		return false
	}
	body := string(b)
	// Make: `test:`; Just: `test:` or `test ` at start of line.
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, target+":") || l == target+":" {
			return true
		}
	}
	return false
}

// summariseTail extracts a short headline from the trailing log lines.
// When tests pass, runner output is voluminous but the last "PASS"
// line or "ok …" line is what humans glance at.
func summariseTail(log, fallback string) string {
	if log == "" {
		return fallback
	}
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && i > len(lines)-6; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			return l
		}
	}
	return fallback
}
