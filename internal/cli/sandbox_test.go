package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
