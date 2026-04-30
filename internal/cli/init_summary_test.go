package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	// Side-effect: register every recipe so runInit --all sees the
	// production catalog when TestInit_SummaryJSONFlag drives it.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

// TestInitSummary_JSONRoundTrip pins that an InitSummary survives a
// JSON encode/decode round-trip with structural equality. Chat-onboard
// (parallel branch) takes a hard dependency on this — the JSON it
// reads back from `--summary-json` must match the in-process struct
// any other code path produced.
func TestInitSummary_JSONRoundTrip(t *testing.T) {
	original := InitSummary{
		AppliedRecipes: []RecipeApply{
			{Name: "claude-md", Category: "agents", Status: RecipeStatusApplied},
			{Name: "license", Category: "governance", Status: RecipeStatusFailed, Error: "needs holder"},
			{Name: "dependabot", Category: "supply-chain", Status: RecipeStatusAlreadyPresent},
		},
		SkippedRecipes: []RecipeSkip{
			{Name: "codeowners", Reason: "missing-required-option"},
			{Name: "release-please", Reason: "not-core"},
		},
		PendingActions: []string{"install gh CLI to use the github playbook"},
		Generated:      map[string]string{"/repo/CLAUDE.md": "clawtool"},
		NextSteps: []string{
			"Run `clawtool recipe status` to confirm what landed.",
			"Re-run `clawtool init` interactively for failed recipes.",
		},
	}

	var buf bytes.Buffer
	if err := original.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	// Sanity: it's actually compact JSON (one trailing newline,
	// otherwise no indentation) so chat-onboard's stdout grep stays
	// trivial.
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("encoded JSON should end with newline; got %q", buf.String())
	}

	var decoded InitSummary
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v\npayload: %s", err, buf.String())
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Errorf("round-trip mismatch:\noriginal: %+v\ndecoded:  %+v", original, decoded)
	}

	// Spot-check tagged keys are present so the chat-onboard branch
	// can pin them in its consumer tests.
	for _, key := range []string{
		`"applied_recipes"`,
		`"skipped_recipes"`,
		`"pending_actions"`,
		`"generated"`,
		`"next_steps"`,
		`"status":"applied"`,
		`"status":"failed"`,
		`"status":"already-present"`,
	} {
		if !strings.Contains(buf.String(), key) {
			t.Errorf("encoded JSON missing %s; payload: %s", key, buf.String())
		}
	}
}

// TestInitSummary_ChatRender pins the markdown completion paragraph
// for a representative summary. The chat-onboard branch reads this
// verbatim — drift here is a breaking-change signal.
func TestInitSummary_ChatRender(t *testing.T) {
	s := InitSummary{
		AppliedRecipes: []RecipeApply{
			{Name: "claude-md", Status: RecipeStatusApplied},
			{Name: "conventional-commits-ci", Status: RecipeStatusApplied},
			{Name: "dependabot", Status: RecipeStatusAlreadyPresent},
			{Name: "promptfoo-redteam", Status: RecipeStatusAlreadyPresent},
		},
		NextSteps: []string{
			"Run `clawtool recipe status` to confirm what landed.",
			"Run `clawtool source add postgres` for DB MCP.",
			"Drop a third step here — should not appear.",
		},
	}

	got := s.ChatRender()
	want := strings.Join([]string{
		"✓ 2 recipes applied: claude-md, conventional-commits-ci",
		"○ 2 already present (idempotent skip)",
		"→ Suggested next: Run `clawtool recipe status` to confirm what landed.; Run `clawtool source add postgres` for DB MCP.",
	}, "\n")

	if got != want {
		t.Errorf("ChatRender mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}

	// Empty summary should render the zero-counts header without a
	// trailing "Suggested next" line.
	zero := InitSummary{}.ChatRender()
	zeroWant := "✓ 0 recipes applied\n○ 0 already present (idempotent skip)"
	if zero != zeroWant {
		t.Errorf("empty ChatRender want %q, got %q", zeroWant, zero)
	}
}

// TestInit_SummaryJSONFlag drives `runInit --all --summary-json`
// against a tmp repo and asserts stdout parses as a single JSON
// document with the expected fields populated. Pinned against the
// production recipe catalog (see _ import above).
func TestInit_SummaryJSONFlag(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := &bytes.Buffer{}
	app := &App{
		Stdout:     out,
		Stderr:     &bytes.Buffer{},
		ConfigPath: filepath.Join(dir, "config.toml"),
	}

	rc := app.runInit([]string{"--all", "--summary-json"})
	if rc != 0 {
		t.Fatalf("runInit exit = %d, want 0; stdout: %s", rc, out.String())
	}

	// Stdout must be parseable JSON — no banner, no human lines.
	var got InitSummary
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\npayload:\n%s", err, out.String())
	}

	// At least the file-writing Core recipes the runInitAll test
	// pins must show up as applied.
	wantApplied := map[string]bool{
		"claude-md":               false,
		"conventional-commits-ci": false,
		"promptfoo-redteam":       false,
	}
	for _, r := range got.AppliedRecipes {
		if r.Status == RecipeStatusApplied {
			if _, ok := wantApplied[r.Name]; ok {
				wantApplied[r.Name] = true
			}
		}
	}
	for name, present := range wantApplied {
		if !present {
			t.Errorf("expected applied recipe %q in summary; got: %+v", name, got.AppliedRecipes)
		}
	}

	// NextSteps must surface at least one bullet when anything
	// applied — the chat consumer relies on this for the "→
	// Suggested next" line.
	if got.AppliedCount() > 0 && len(got.NextSteps) == 0 {
		t.Errorf("NextSteps empty but %d recipes applied", got.AppliedCount())
	}

	// Humans-only "clawtool init —" banner must NOT appear in JSON
	// mode — chat consumers grep raw stdout.
	if strings.Contains(out.String(), "clawtool init —") {
		t.Errorf("--summary-json must suppress the banner; got: %s", out.String())
	}
}
