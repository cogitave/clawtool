package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestParseBridgeAddArgs covers every input combination the
// dispatcher might pass: bare family, --json before family,
// --json after family, --format=json long-form, missing family,
// duplicate family.
func TestParseBridgeAddArgs(t *testing.T) {
	cases := []struct {
		name       string
		argv       []string
		wantFamily string
		wantJSON   bool
		wantErr    bool
	}{
		{"bare-family", []string{"codex"}, "codex", false, false},
		{"json-after-family", []string{"codex", "--json"}, "codex", true, false},
		{"json-before-family", []string{"--json", "codex"}, "codex", true, false},
		{"format-json-alias", []string{"codex", "--format=json"}, "codex", true, false},
		{"missing-family", []string{"--json"}, "", false, true},
		{"empty", []string{}, "", false, true},
		{"two-positionals", []string{"codex", "gemini"}, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fam, asJSON, err := parseBridgeAddArgs(c.argv)
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if fam != c.wantFamily {
				t.Errorf("family = %q, want %q", fam, c.wantFamily)
			}
			if asJSON != c.wantJSON {
				t.Errorf("asJSON = %v, want %v", asJSON, c.wantJSON)
			}
		})
	}
}

// TestBridgeAddJSON_UnknownFamily exercises the error-rendering
// path: passing an unknown family produces a structured JSON
// error result on stdout (with the documented snake_case shape)
// and returns exit code 1.
//
// Doesn't test the install happy path — that calls setup.Apply
// which spawns network-dependent npm work; out of scope for a
// unit test. The JSON wire shape is the main contract this test
// pins.
func TestBridgeAddJSON_UnknownFamily(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	rc := app.bridgeAddJSON("not-a-real-family", "add")
	if rc != 1 {
		t.Errorf("rc = %d, want 1 (unknown family)", rc)
	}

	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Fatalf("expected JSON object on stdout; got %q", body)
	}
	for _, lit := range []string{`"family":`, `"action":`, `"installed":`, `"verify_ok":`, `"error":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}

	var got bridgeAddJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got.Family != "not-a-real-family" {
		t.Errorf("family = %q, want not-a-real-family", got.Family)
	}
	if got.Action != "add" {
		t.Errorf("action = %q, want add", got.Action)
	}
	if got.Installed {
		t.Error("installed = true on unknown family; want false")
	}
	if !strings.Contains(got.Error, "unknown family") {
		t.Errorf("error should mention 'unknown family'; got %q", got.Error)
	}
}

// TestBridgeAddJSON_StableShape confirms the output is an object
// (not an array) — bridge add operates on a single family per
// invocation. `jq '.family'` consumers rely on object shape.
func TestBridgeAddJSON_StableShape(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	_ = app.bridgeAddJSON("not-a-real-family", "add")
	body := strings.TrimSpace(out.String())
	if len(body) == 0 || body[0] != '{' {
		t.Errorf("expected object (starts with '{'); got: %q", body)
	}
}

// TestBridgeAddJSON_UpgradeAction ensures the action field
// distinguishes "add" from "upgrade" — same install logic, but
// scripts that log claim/release events should see the right
// verb in the structured payload.
func TestBridgeAddJSON_UpgradeAction(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	_ = app.bridgeAddJSON("not-a-real-family", "upgrade")
	var got bridgeAddJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Action != "upgrade" {
		t.Errorf("action = %q, want upgrade", got.Action)
	}
}
