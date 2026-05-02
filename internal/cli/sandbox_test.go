package cli

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/sandbox"
)

// TestSandboxDoctor_HumanOutput preserves the existing
// "ENGINE AVAILABLE" table + "selected: <name>" footer so the
// unscripted operator workflow keeps working.
func TestSandboxDoctor_HumanOutput(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "doctor"}); rc != 0 {
		t.Fatalf("doctor rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{
		"ENGINE",
		"AVAILABLE",
		"selected:",
		// The noop engine is always registered + always
		// reports Available=true, so it must appear in
		// every host's output regardless of the platform.
		"noop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestSandboxDoctor_JSONOutput emits a parseable {engines,
// selected} object whose snake_case keys match the project's
// wire convention. Continues the JSON wire-contract series
// alongside `agents status --json`, `rules list --json`, etc.
func TestSandboxDoctor_JSONOutput(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "doctor", "--json"}); rc != 0 {
		t.Fatalf("doctor --json rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("expected JSON object; got: %q", body)
	}
	for _, lit := range []string{`"engines":`, `"selected":`, `"name":`, `"available":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got struct {
		Engines []struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
		} `json:"engines"`
		Selected string `json:"selected"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(got.Engines) == 0 {
		t.Fatal("engines should not be empty (noop is always present)")
	}
	if got.Selected == "" {
		t.Error("selected should always be populated")
	}
	// noop is always registered + Available=true, so it must
	// appear in every host's output.
	foundNoop := false
	for _, e := range got.Engines {
		if e.Name == "noop" {
			foundNoop = true
			if !e.Available {
				t.Errorf("noop should always be Available=true; got %+v", e)
			}
			break
		}
	}
	if !foundNoop {
		t.Errorf("noop engine missing from JSON: %+v", got.Engines)
	}
}

// TestSandboxDoctor_JSONStableShape confirms the JSON path
// emits an OBJECT (not an array) — doctor returns a singular
// status snapshot, not a list. `jq '.selected'` consumers rely
// on object shape.
func TestSandboxDoctor_JSONStableShape(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "doctor", "--json"}); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Errorf("expected object (starts with '{'); got: %q", body)
	}
}

// TestSandboxDoctor_FormatJsonAlias confirms the long form
// `--format=json` is also accepted (matches `clawtool version
// --json` / `--format=json` parity).
func TestSandboxDoctor_FormatJsonAlias(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "doctor", "--format=json"}); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimSpace(out.String())
	if !strings.HasPrefix(body, "{") {
		t.Errorf("--format=json should also produce JSON; got %q", body)
	}
}

// TestSandboxList_EmptyJSON pins the empty-state contract for
// `sandbox list --format json`: a fresh config emits `[]\n` so
// a `clawtool sandbox list --format json | jq '. | length'`
// pipeline returns 0 instead of choking on the human banner.
// Sister of TestSourceList_EmptyJSON (commit 18aed7e).
func TestSandboxList_EmptyJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "list", "--format", "json"}); rc != 0 {
		t.Fatalf("list --format json rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimSpace(out.String())
	if body != "[]" {
		t.Errorf("expected '[]' on empty-state JSON; got %q", body)
	}
	var arr []map[string]string
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array; got %d entries", len(arr))
	}
}

// TestSandboxList_EmptyTSV exercises the TSV path's empty-state
// — a header-only line means `awk 'NR>1{...}'` consumers stop
// cleanly without seeing the human banner mid-pipe.
func TestSandboxList_EmptyTSV(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "list", "--format", "tsv"}); rc != 0 {
		t.Fatalf("list --format tsv rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 header line on empty-state TSV; got %d: %q", len(lines), body)
	}
	cells := strings.Split(lines[0], "\t")
	if len(cells) != 3 || cells[0] != "PROFILE" || cells[1] != "ENGINE" || cells[2] != "DESCRIPTION" {
		t.Errorf("expected PROFILE\\tENGINE\\tDESCRIPTION header; got %q", lines[0])
	}
}

// TestSandboxList_EmptyTable preserves the actionable hint so
// interactive shell users running `sandbox list` in a fresh
// checkout still get the docs pointer instead of a bare header.
func TestSandboxList_EmptyTable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "list"}); rc != 0 {
		t.Fatalf("list rc=%d, stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "no sandbox profiles configured") {
		t.Errorf("expected human banner on empty-state table; got %q", out.String())
	}
	if !strings.Contains(out.String(), "docs/sandbox.md") {
		t.Errorf("expected docs pointer in human banner; got %q", out.String())
	}
}

// TestSandboxDoctor_AppendsHintOnMissing verifies the ADR-020
// §Resolved (2026-05-02) gate: when an engine is reported as
// MISSING, doctor's human output appends the operator-facing
// install hint (NOT a sudo-driven auto-install). We invoke the
// real doctor flow and cross-check against sandbox.AvailableEngines
// + sandbox.InstallHint — whatever engines this host happens to
// be missing, the matching hints must show up. Test is host-aware
// so it works on every CI runner without ad-hoc skips.
func TestSandboxDoctor_AppendsHintOnMissing(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"sandbox", "doctor"}); rc != 0 {
		t.Fatalf("doctor rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()

	// Build the set of engines this host reports MISSING and
	// for which an install hint is defined. If at least one
	// such engine exists, the doctor output MUST include the
	// "<engine> missing — install hint:" header for it AND a
	// distinctive substring from the hint body.
	statuses := sandbox.AvailableEngines()
	checkedAny := false
	for _, st := range statuses {
		if st.Available {
			continue
		}
		hint := sandbox.InstallHint(runtime.GOOS, st.Name)
		if hint == "" {
			continue
		}
		checkedAny = true
		header := st.Name + " missing — install hint:"
		if !strings.Contains(body, header) {
			t.Errorf("doctor output missing hint header %q for unavailable engine\n--- body ---\n%s",
				header, body)
		}
		// Pick the first non-empty hint line as a unique
		// fingerprint and require it to appear in the output.
		for _, line := range strings.Split(hint, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !strings.Contains(body, line) {
				t.Errorf("doctor output missing hint line %q for engine %s\n--- body ---\n%s",
					line, st.Name, body)
			}
			break
		}
	}

	// Sanity: on every supported CI host (linux without docker
	// daemon, darwin without docker, windows-with-just-noop)
	// at least one engine is unavailable + has a hint. If this
	// branch never fired, the test would silently pass on a
	// host that's somehow installed every engine — that's fine
	// in production but we want a clear signal in CI. Skip
	// rather than fail so the test stays portable.
	if !checkedAny {
		t.Skip("no missing engines with defined hints on this host — nothing to verify (test is host-aware)")
	}
}
