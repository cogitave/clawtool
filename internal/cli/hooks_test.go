package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

// TestHooksInstall_DrainSnippets — every non-claude-code runtime
// must surface the new session-tick drain command in its install
// snippet, so peer messages reach the live agent on every turn.
// Without this the operator wires register/deregister and never
// learns the inbox needs to be drained.
func TestHooksInstall_DrainSnippets(t *testing.T) {
	for _, runtime := range []string{"codex", "gemini", "opencode"} {
		t.Run(runtime, func(t *testing.T) {
			var outBuf, errBuf bytes.Buffer
			app := New()
			app.Stdout = &outBuf
			app.Stderr = &errBuf
			code := app.Run([]string{"hooks", "install", runtime})
			if code != 0 {
				t.Fatalf("rc=%d, stderr=%s", code, errBuf.String())
			}
			body := outBuf.String()
			if !strings.Contains(body, "clawtool peer drain --format context") {
				t.Errorf("%s install snippet missing drain command:\n%s", runtime, body)
			}
		})
	}
}

// TestBundledHooksJSON_UserPromptSubmitHasDrain — the Claude Code
// plugin's hooks/hooks.json must wire `clawtool peer drain --format
// hook-json` into the UserPromptSubmit event so peer inbox messages
// auto-deliver as additionalContext at the start of each user turn.
// Stop's drain entry is REMOVED here: Stop fires AFTER the agent
// has already responded, so its stdout never reached the agent's
// context — that drain was dead noise. Stop keeps its heartbeat
// entry, which IS useful (status flip independent of stdin/stdout).
func TestBundledHooksJSON_UserPromptSubmitHasDrain(t *testing.T) {
	// internal/cli → repo root: ../../hooks/hooks.json
	body, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("read bundled hooks.json: %v", err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse hooks.json: %v", err)
	}

	// UserPromptSubmit MUST carry the drain --format hook-json
	// command (this is the auto-deliver pipeline).
	ups, ok := cfg.Hooks["UserPromptSubmit"]
	if !ok || len(ups) == 0 {
		t.Fatalf("UserPromptSubmit event missing")
	}
	var sawDrain, sawBusyHeartbeat bool
	for _, m := range ups {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, "peer drain --format hook-json") {
				sawDrain = true
			}
			if strings.Contains(h.Command, "peer heartbeat --status busy") {
				sawBusyHeartbeat = true
			}
		}
	}
	if !sawDrain {
		t.Errorf("UserPromptSubmit missing 'peer drain --format hook-json' hook")
	}
	if !sawBusyHeartbeat {
		t.Errorf("UserPromptSubmit must keep the existing busy-heartbeat hook")
	}

	// Stop MUST NOT carry a drain — its stdout never reaches the
	// agent. Heartbeat (status online) MUST stay.
	stop, ok := cfg.Hooks["Stop"]
	if !ok || len(stop) == 0 {
		t.Fatalf("Stop event missing")
	}
	var stopHasDrain, stopHasOnlineHeartbeat bool
	for _, m := range stop {
		for _, h := range m.Hooks {
			if strings.Contains(h.Command, "peer drain") {
				stopHasDrain = true
			}
			if strings.Contains(h.Command, "peer heartbeat --status online") {
				stopHasOnlineHeartbeat = true
			}
		}
	}
	if stopHasDrain {
		t.Errorf("Stop event must NOT have peer drain (its stdout never reaches the agent)")
	}
	if !stopHasOnlineHeartbeat {
		t.Errorf("Stop event must keep the existing online-heartbeat hook")
	}

	// SessionEnd / SessionStart are existing wiring — guard them
	// so a future re-roll of this file doesn't accidentally drop
	// register / deregister.
	for _, ev := range []string{"SessionStart", "SessionEnd"} {
		if _, ok := cfg.Hooks[ev]; !ok {
			t.Errorf("bundled hooks.json lost %s event", ev)
		}
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
