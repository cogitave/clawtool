package rules

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) expr {
	t.Helper()
	e, err := parseExpr(src)
	if err != nil {
		t.Fatalf("parseExpr(%q): %v", src, err)
	}
	return e
}

func TestParse_Primitives(t *testing.T) {
	cases := []string{
		`changed("README.md")`,
		`commit_message_contains("feat:")`,
		`tool_call_count("Edit") > 5`,
		`tool_call_count("Bash") >= 1`,
		`arg("instance") == "opencode"`,
		`guardians_check("plan")`,
		`true`,
		`false`,
	}
	for _, c := range cases {
		if _, err := parseExpr(c); err != nil {
			t.Errorf("parseExpr(%q) failed: %v", c, err)
		}
	}
}

func TestParse_Composite(t *testing.T) {
	cases := []string{
		`changed("a") and changed("b")`,
		`changed("a") OR changed("b")`,
		`changed("a") && not changed("b")`,
		`(changed("a") or changed("b")) and not changed("c")`,
		`tool_call_count("Edit") > 0 AND not changed("README.md")`,
	}
	for _, c := range cases {
		if _, err := parseExpr(c); err != nil {
			t.Errorf("parseExpr(%q) failed: %v", c, err)
		}
	}
}

func TestParse_Errors(t *testing.T) {
	parseErrCases := []string{
		``,
		`changed`,     // missing args
		`changed(`,    // unterminated
		`changed("a"`, // missing close paren
	}
	for i, c := range parseErrCases {
		if _, err := parseExpr(c); err == nil {
			t.Errorf("parseErr[%d] %q: expected parse error, got nil", i, c)
		}
	}
	// These parse cleanly but error at eval time (missing comparison
	// for tool_call_count, unknown predicate). Important contract:
	// keep parser permissive so loader's pre-parse step doesn't
	// reject runtime-resolvable mistakes.
	evalErrCases := []string{
		`tool_call_count("E")`,
		`unknown_predicate("x")`,
	}
	for i, c := range evalErrCases {
		e, err := parseExpr(c)
		if err != nil {
			t.Fatalf("evalErr[%d] %q: parse failed: %v", i, c, err)
		}
		_, _, err = e.eval(Context{})
		if err == nil {
			t.Errorf("evalErr[%d] %q: expected eval error, got nil", i, c)
		}
	}
}

func TestEval_ChangedGlob(t *testing.T) {
	ctx := Context{
		Event:        EventPostEdit,
		ChangedPaths: []string{"internal/tools/core/bash.go", "README.md"},
	}
	matches := map[string]bool{
		`changed("README.md")`:                true,
		`changed("internal/tools/core/*.go")`: true,
		`changed("docs/**/*.md")`:             false,
		`changed("nonexistent.txt")`:          false,
	}
	for src, want := range matches {
		e := mustParse(t, src)
		got, _, err := e.eval(ctx)
		if err != nil {
			t.Fatalf("eval %q: %v", src, err)
		}
		if got != want {
			t.Errorf("eval %q = %v, want %v", src, got, want)
		}
	}
}

func TestEval_CommitMessage(t *testing.T) {
	ctx := Context{
		Event:         EventPreCommit,
		CommitMessage: "feat: add hermes bridge\n\nCo-Authored-By: Claude <noreply@anthropic.com>",
	}
	if got, _, _ := mustParse(t, `commit_message_contains("Co-Authored-By")`).eval(ctx); !got {
		t.Error("expected Co-Authored-By detection")
	}
	if got, _, _ := mustParse(t, `commit_message_contains("Signed-off-by")`).eval(ctx); got {
		t.Error("expected Signed-off-by miss")
	}
}

func TestEval_ToolCallCount(t *testing.T) {
	ctx := Context{
		ToolCalls: map[string]int{"Edit": 5, "Bash": 0},
	}
	cases := map[string]bool{
		`tool_call_count("Edit") > 3`:  true,
		`tool_call_count("Edit") > 10`: false,
		`tool_call_count("Edit") == 5`: true,
		`tool_call_count("Bash") == 0`: true,
		`tool_call_count("Edit") != 5`: false,
		`tool_call_count("Ghost") > 0`: false, // missing key = 0
	}
	for src, want := range cases {
		got, _, err := mustParse(t, src).eval(ctx)
		if err != nil {
			t.Fatalf("eval %q: %v", src, err)
		}
		if got != want {
			t.Errorf("eval %q = %v, want %v", src, got, want)
		}
	}
}

func TestEval_LogicalOps(t *testing.T) {
	ctx := Context{
		Event:        EventPostEdit,
		ChangedPaths: []string{"internal/tools/core/bash.go"},
	}
	cases := map[string]bool{
		`changed("internal/**/*.go") and changed("README.md")`:                  false,
		`changed("internal/**/*.go") or changed("README.md")`:                   true,
		`changed("internal/**/*.go") and not changed("docs/**/*.md")`:           true,
		`(changed("nonexistent") or changed("internal/**/*.go")) and not false`: true,
	}
	for src, want := range cases {
		got, _, err := mustParse(t, src).eval(ctx)
		if err != nil {
			t.Fatalf("eval %q: %v", src, err)
		}
		if got != want {
			t.Errorf("eval %q = %v, want %v", src, got, want)
		}
	}
}

func TestEval_GuardiansCheckStub(t *testing.T) {
	// Phase-1 contract: guardians_check ALWAYS returns true so a
	// pre_send rule wired against it never blocks. Operators can
	// codify the rule shape today; phase-2 flips the verdict to
	// the real Z3-SAT result without changing the surface.
	cases := []struct {
		name string
		args map[string]string
	}{
		{"empty plan arg present", map[string]string{"plan": ""}},
		{"plan arg with body", map[string]string{"plan": "draft: edit README, run tests"}},
		{"plan arg missing entirely", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := Context{Event: EventPreSend, Args: tc.args}
			got, why, err := mustParse(t, `guardians_check("plan")`).eval(ctx)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if !got {
				t.Errorf("phase-1 stub must always return true, got false (why=%q)", why)
			}
		})
	}
}

func TestEvaluate_GuardiansCheckNeverBlocks(t *testing.T) {
	// End-to-end: a block-severity rule shaped like the recipe
	// template (`condition = guardians_check("plan")`) must
	// NEVER block today. clawtool's rule engine treats a
	// condition that evaluates to TRUE as "rule passed", and
	// the phase-1 stub always returns true → this rule always
	// passes. Phase-2 will return false (with an UNSAT-core
	// reason) when the plan violates a taint-flow invariant,
	// flipping this rule to a real block — but the rule shape
	// itself stays identical across the two phases.
	rules := []Rule{
		{
			Name:      "guardians-presend",
			When:      EventPreSend,
			Condition: `guardians_check("plan")`,
			Severity:  SeverityBlock,
			Hint:      "phase-1 stub; phase-2 wires Z3 + taint engine.",
		},
	}
	ctx := Context{
		Event: EventPreSend,
		Args:  map[string]string{"plan": "anything goes today"},
	}
	v := Evaluate(rules, ctx)
	if v.IsBlocked() {
		t.Fatalf("phase-1 stub must not block; got blocked=%+v", v.Blocked)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("phase-1 stub must not warn; got warnings=%+v", v.Warnings)
	}
}

func TestEvaluate_BlocksAndWarnings(t *testing.T) {
	rules := []Rule{
		{
			Name:      "no-coauthor",
			When:      EventPreCommit,
			Condition: `not commit_message_contains("Co-Authored-By")`,
			Severity:  SeverityBlock,
			Hint:      "Operator memory rule — never attribute to AI in commits.",
		},
		{
			Name:      "readme-current",
			When:      EventPreCommit,
			Condition: `not (changed("internal/tools/core/*.go") and not changed("README.md"))`,
			Severity:  SeverityWarn,
			Hint:      "Update README when shipping a new core tool.",
		},
		{
			Name:      "off-rule",
			When:      EventPreCommit,
			Condition: `true`,
			Severity:  SeverityOff,
		},
	}
	ctx := Context{
		Event:         EventPreCommit,
		ChangedPaths:  []string{"internal/tools/core/bash.go"},
		CommitMessage: "feat: x\n\nCo-Authored-By: Claude",
	}
	v := Evaluate(rules, ctx)

	if !v.IsBlocked() {
		t.Errorf("expected blocked, got %+v", v)
	}
	// no-coauthor blocks (Co-Authored-By present)
	// readme-current warns (core changed but README didn't)
	// off-rule skipped
	if len(v.Blocked) != 1 || v.Blocked[0].Rule != "no-coauthor" {
		t.Errorf("expected 1 block on no-coauthor, got %+v", v.Blocked)
	}
	if len(v.Warnings) != 1 || v.Warnings[0].Rule != "readme-current" {
		t.Errorf("expected 1 warn on readme-current, got %+v", v.Warnings)
	}
	for _, r := range v.Results {
		if r.Rule == "off-rule" {
			t.Errorf("off-severity rule should be skipped, got: %+v", r)
		}
	}
}

func TestParseBytes_LoaderRoundTrip(t *testing.T) {
	body := []byte(`
[[rule]]
name = "no-coauthor"
when = "pre_commit"
severity = "block"
condition = 'not commit_message_contains("Co-Authored-By")'
hint = "Never attribute to AI."

[[rule]]
name = "readme-current"
when = "pre_commit"
condition = 'not (changed("internal/tools/core/*.go") and not changed("README.md"))'
hint = "Update README on core tool changes."
`)
	rules, err := ParseBytes(body)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	// Default severity for second rule (no severity in TOML) → warn
	if rules[1].Severity != SeverityWarn {
		t.Errorf("default severity = %q, want %q", rules[1].Severity, SeverityWarn)
	}
}

func TestParseBytes_InvalidEvent(t *testing.T) {
	body := []byte(`
[[rule]]
name = "bad"
when = "wat_event"
severity = "warn"
condition = "true"
`)
	_, err := ParseBytes(body)
	if err == nil || !strings.Contains(err.Error(), "invalid 'when'") {
		t.Errorf("expected 'invalid when' error, got: %v", err)
	}
}

func TestParseBytes_InvalidCondition(t *testing.T) {
	body := []byte(`
[[rule]]
name = "bad-cond"
when = "post_edit"
severity = "warn"
condition = "changed( unterminated"
`)
	_, err := ParseBytes(body)
	if err == nil || !strings.Contains(err.Error(), "condition") {
		t.Errorf("expected condition parse error, got: %v", err)
	}
}
