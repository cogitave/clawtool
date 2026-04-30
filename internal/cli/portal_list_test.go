package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestPortalList_EmptyJSON pins the empty-state contract for
// `portal list --format json`: a fresh config emits `[]\n` so a
// `clawtool portal list --format json | jq '. | length'`
// pipeline returns 0 instead of choking on the human banner.
// Sister of TestSourceList_EmptyJSON (commit 18aed7e) and
// TestSandboxList_EmptyJSON (commit 83436bf).
func TestPortalList_EmptyJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list", "--format", "json"}); rc != 0 {
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

// TestPortalList_EmptyTSV exercises the TSV path's empty-state.
// A header-only line means `awk 'NR>1{...}'` consumers stop
// cleanly without seeing the human banner mid-pipe.
func TestPortalList_EmptyTSV(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list", "--format", "tsv"}); rc != 0 {
		t.Fatalf("list --format tsv rc=%d, stderr=%s", rc, errb.String())
	}
	body := strings.TrimRight(out.String(), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 header line on empty-state TSV; got %d: %q", len(lines), body)
	}
	cells := strings.Split(lines[0], "\t")
	if len(cells) != 3 || cells[0] != "NAME" || cells[1] != "BASE_URL" || cells[2] != "AUTH_COOKIES" {
		t.Errorf("expected NAME\\tBASE_URL\\tAUTH_COOKIES header; got %q", lines[0])
	}
}

// TestPortalList_EmptyTable preserves the actionable hint so
// interactive shell users running `portal list` in a fresh
// checkout still get the `clawtool portal add` pointer instead
// of a bare header.
func TestPortalList_EmptyTable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb}

	if rc := app.Run([]string{"portal", "list"}); rc != 0 {
		t.Fatalf("list rc=%d, stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "no portals configured") {
		t.Errorf("expected human banner on empty-state table; got %q", out.String())
	}
	if !strings.Contains(out.String(), "clawtool portal add") {
		t.Errorf("expected actionable add pointer in human banner; got %q", out.String())
	}
}
