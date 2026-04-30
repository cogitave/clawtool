package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// seedSourceInspect populates the App's config with a minimal stdio
// source so the inspect verb has something to look up. Returns the
// instance name written.
func seedSourceInspect(t *testing.T, app *App) string {
	t.Helper()
	cfg := config.Default()
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.Source{}
	}
	cfg.Sources["fake-src"] = config.Source{
		Type:    "mcp",
		Command: []string{"node", "fake-server.js"},
	}
	if err := cfg.Save(app.Path()); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return "fake-src"
}

// TestSourceInspect_DryRunArgvShape pins the exact npx invocation
// the verb builds. Drift here would change the ABI between clawtool
// and the npm-published @modelcontextprotocol/inspector — break this
// test on purpose if the inspector's `--cli` flag ever changes.
func TestSourceInspect_DryRunArgvShape(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	seedSourceInspect(t, app)

	rc := app.Run([]string{"source", "inspect", "fake-src", "--dry-run"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{
		"(dry-run) would run:",
		"npx",
		"-y",
		"@modelcontextprotocol/inspector",
		"--cli",
		"node",
		"fake-server.js",
		"--method",
		"tools/list",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestSourceInspect_NotFound exercises the ErrSourceNotFound branch.
// Pinning the wrapped sentinel (visible in stderr text) lets pipelines
// branch on the not-found case without parsing the inspector.
func TestSourceInspect_NotFound(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	rc := app.Run([]string{"source", "inspect", "ghost-src"})
	if rc != 1 {
		t.Fatalf("rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), ErrSourceNotFound.Error()) {
		t.Errorf("stderr missing %q sentinel: %q", ErrSourceNotFound.Error(), errb.String())
	}
	if !strings.Contains(errb.String(), `"ghost-src"`) {
		t.Errorf("stderr should quote the missing instance: %q", errb.String())
	}
	// Belt-and-suspenders: the package-level sentinel is what
	// future callers of an exported helper would errors.Is against.
	if !errors.Is(ErrSourceNotFound, ErrSourceNotFound) {
		t.Error("ErrSourceNotFound is not its own root via errors.Is")
	}
}

// TestSourceInspect_NoCommand confirms a source missing both stdio
// command and HTTP url surfaces a structured error rather than
// silently building a malformed npx invocation.
func TestSourceInspect_NoCommand(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	cfg := config.Default()
	cfg.Sources = map[string]config.Source{
		"empty-src": {Type: "mcp"}, // no Command, no URL.
	}
	if err := cfg.Save(app.Path()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rc := app.Run([]string{"source", "inspect", "empty-src"})
	if rc != 1 {
		t.Fatalf("rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "no stdio command configured") {
		t.Errorf("expected no-command guard, got: %q", errb.String())
	}
}

// TestSourceInspect_TextRendersTools stubs the inspector runner so
// the verb's text-mode renderer can be exercised without actually
// shelling out to npx. Pins the human-readable shape:
// `tools exposed by "<name>" (N):` header + `  <name> — <desc>` rows.
func TestSourceInspect_TextRendersTools(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	seedSourceInspect(t, app)

	stub := `{"tools":[
		{"name":"echo","description":"Echo back the input."},
		{"name":"add","description":"Add two integers."}
	]}`
	old := inspectorRunner
	inspectorRunner = func(_ context.Context, _ []string, _ map[string]string) ([]byte, error) {
		return []byte(stub), nil
	}
	t.Cleanup(func() { inspectorRunner = old })

	rc := app.Run([]string{"source", "inspect", "fake-src"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	body := out.String()
	for _, want := range []string{
		`tools exposed by "fake-src" (2):`,
		"echo — Echo back the input.",
		"add — Add two integers.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("text output missing %q\n--- output ---\n%s", want, body)
		}
	}
}

// TestSourceInspect_JSONPassthrough confirms `--format json` returns
// the inspector's stdout verbatim (parsable as JSON), so pipelines
// see the full inputSchema/annotations the text view drops.
func TestSourceInspect_JSONPassthrough(t *testing.T) {
	app, out, errb, _, _ := newSrcApp(t)
	seedSourceInspect(t, app)

	stub := `{"tools":[{"name":"echo","description":"x","inputSchema":{"type":"object"}}]}`
	old := inspectorRunner
	inspectorRunner = func(_ context.Context, _ []string, _ map[string]string) ([]byte, error) {
		return []byte(stub), nil
	}
	t.Cleanup(func() { inspectorRunner = old })

	rc := app.Run([]string{"source", "inspect", "fake-src", "--format", "json"})
	if rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errb.String())
	}
	var got struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON passthrough: %v\nbody: %s", err, out.String())
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "echo" {
		t.Errorf("unexpected JSON: %+v", got.Tools)
	}
	if got.Tools[0].InputSchema == nil {
		t.Error("inputSchema should be preserved in JSON passthrough")
	}
}

// TestSourceInspect_BadFormat keeps the flag contract tight — only
// text|json are accepted; typos surface as exit 2 with usage text.
func TestSourceInspect_BadFormat(t *testing.T) {
	app, _, errb, _, _ := newSrcApp(t)
	seedSourceInspect(t, app)
	rc := app.Run([]string{"source", "inspect", "fake-src", "--format", "yaml"})
	if rc != 2 {
		t.Errorf("rc=%d, want 2 on bad --format", rc)
	}
	if !strings.Contains(errb.String(), "text|json") {
		t.Errorf("expected format hint, got: %q", errb.String())
	}
}
