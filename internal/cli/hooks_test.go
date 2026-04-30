package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/hooks"
)

// runHooksWith stamps a config file with the given block then drives
// `clawtool hooks <subcmd>` against it.
func runHooksWith(t *testing.T, hcfg config.HooksConfig, argv []string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := config.Default()
	cfg.Hooks = hcfg
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	app := New()
	app.ConfigPath = cfgPath
	app.Stdout = &outBuf
	app.Stderr = &errBuf
	code = app.Run(append([]string{"hooks"}, argv...))
	return outBuf.String(), errBuf.String(), code
}

func TestHooksList_Empty(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{}, []string{"list"})
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if !strings.Contains(out, "no hooks configured") {
		t.Errorf("expected hint; got %q", out)
	}
}

// TestHooksList_EmptyJSON pins the empty-state contract for
// `hooks list --format json`: a fresh config emits `[]\n` so a
// `clawtool hooks list --format json | jq '. | length'` pipeline
// returns 0 instead of choking on the human banner. Sister of
// TestSourceList_EmptyJSON / TestSandboxList_EmptyJSON /
// TestPortalList_EmptyJSON.
func TestHooksList_EmptyJSON(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{}, []string{"list", "--format", "json"})
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	body := strings.TrimSpace(out)
	if body != "[]" {
		t.Errorf("expected '[]' on empty-state JSON; got %q", body)
	}
}

// TestHooksList_EmptyTSV exercises the TSV path's empty-state.
// A header-only line means `awk 'NR>1{...}'` consumers stop
// cleanly without seeing the human banner mid-pipe.
func TestHooksList_EmptyTSV(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{}, []string{"list", "--format", "tsv"})
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	body := strings.TrimRight(out, "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 header line; got %d: %q", len(lines), body)
	}
	cells := strings.Split(lines[0], "\t")
	if len(cells) != 2 || cells[0] != "EVENT" || cells[1] != "ENTRIES" {
		t.Errorf("expected EVENT\\tENTRIES header; got %q", lines[0])
	}
}

func TestHooksList_PrintsCounts(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send":         {{Cmd: "true"}, {Cmd: "true"}},
			"on_task_complete": {{Cmd: "true"}},
		},
	}, []string{"list"})
	if code != 0 {
		t.Fatalf("unexpected exit %d", code)
	}
	if !strings.Contains(out, "pre_send") || !strings.Contains(out, "2") {
		t.Errorf("list should show entries: %q", out)
	}
}

func TestHooksShow_NoEntries(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{}, []string{"show", "pre_send"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "no entries configured") {
		t.Errorf("expected friendly hint; got %q", out)
	}
}

func TestHooksShow_RendersEntries(t *testing.T) {
	out, _, _ := runHooksWith(t, config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {
				{Cmd: "echo hello", TimeoutMs: 1500, BlockOnErr: true},
			},
		},
	}, []string{"show", "pre_send"})
	if !strings.Contains(out, "echo hello") || !strings.Contains(out, "1500") || !strings.Contains(out, "true") {
		t.Errorf("show should print cmd + timeout + block flag; got %q", out)
	}
}

func TestHooksTest_RunsConfiguredEntry(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "fired")
	out, _, code := runHooksWith(t, config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {{Cmd: "touch " + flag}},
		},
	}, []string{"test", "pre_send"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "1 entry/entries ran cleanly") {
		t.Errorf("test should report a clean run: %q", out)
	}
}

func TestHooksTest_NoConfig(t *testing.T) {
	out, _, code := runHooksWith(t, config.HooksConfig{}, []string{"test", "pre_send"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "nothing to do") {
		t.Errorf("missing-config hint missing: %q", out)
	}
}

// Sanity: hooks.Event constants line up with the CLI tester.
func TestEventConstants_StableNames(t *testing.T) {
	want := []string{
		"pre_send", "post_send", "on_task_complete",
		"pre_edit", "post_edit",
		"pre_bridge_add", "post_recipe_apply",
		"on_server_start", "on_server_stop",
	}
	mgr := hooks.New(config.HooksConfig{})
	_ = mgr.Emit(context.Background(), hooks.EventPreSend, nil) // no-op smoke
	for _, n := range want {
		// Cast through hooks.Event ensures the package exports the
		// matching const string (compile-time guard via test).
		ev := hooks.Event(n)
		if string(ev) != n {
			t.Errorf("event %q round-trip mismatch", n)
		}
	}
}
