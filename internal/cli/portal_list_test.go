package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestPortalList_EmptyJSON pins the empty-state contract for
// `portal list --format json`: even with no [portals.*] stanzas
// configured, the bifrost compile-time driver self-registers, so
// the JSON output is a one-element array (not `[]`). Pipelines
// can still iterate cleanly via `jq '. | length'`.
//
// Sister of TestSourceList_EmptyJSON (commit 18aed7e) and
// TestSandboxList_EmptyJSON (commit 83436bf) — same pattern, with
// the wrinkle that portals now expose at least one row for free
// thanks to the in-process driver registry.
func TestPortalList_EmptyJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list", "--format", "json"}); rc != 0 {
		t.Fatalf("list --format json rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimSpace(out.String())
	var arr []map[string]string
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	// At least one row from the bifrost driver — its name + status
	// are stable contract.
	var foundBifrost bool
	for _, row := range arr {
		if row["name"] == "bifrost" && row["status"] == "deferred" {
			foundBifrost = true
		}
	}
	if !foundBifrost {
		t.Errorf("expected bifrost (deferred) row in JSON output; got %+v", arr)
	}
}

// TestPortalList_EmptyTSV exercises the TSV path with the new
// STATUS column. Header is NAME\tSTATUS\tBASE_URL\tAUTH_COOKIES;
// at least one data line is the bifrost driver row.
func TestPortalList_EmptyTSV(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list", "--format", "tsv"}); rc != 0 {
		t.Fatalf("list --format tsv rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + at least 1 driver row; got %d lines: %q", len(lines), body)
	}
	cells := strings.Split(lines[0], "\t")
	if len(cells) != 4 || cells[0] != "NAME" || cells[1] != "STATUS" || cells[2] != "BASE_URL" || cells[3] != "AUTH_COOKIES" {
		t.Errorf("expected NAME\\tSTATUS\\tBASE_URL\\tAUTH_COOKIES header; got %q", lines[0])
	}
	var foundBifrost bool
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "bifrost\tdeferred\t") {
			foundBifrost = true
		}
	}
	if !foundBifrost {
		t.Errorf("expected bifrost\\tdeferred row in TSV output; got %q", body)
	}
}

// TestPortalList_EmptyTable preserves the actionable hint for the
// fully-empty case. Drivers still surface in table mode, so the
// banner appears only when neither configured portals nor drivers
// exist — i.e. it never fires in the default build, but the
// hint is still wired so a hypothetical no-driver build prints
// the actionable next-step instead of a bare header.
func TestPortalList_EmptyTable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list"}); rc != 0 {
		t.Fatalf("list rc=%d, stderr=%s", rc, errb.String())
	}
	// With the bifrost driver registered, the table mode shows
	// the driver row instead of the empty-state banner. Assert
	// the row is rendered with the deferred status visible.
	body := out.String()
	if !strings.Contains(body, "bifrost") {
		t.Errorf("expected bifrost driver row in table output; got %q", body)
	}
	if !strings.Contains(body, "deferred") {
		t.Errorf("expected deferred status in table output; got %q", body)
	}
}
