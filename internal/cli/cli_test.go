package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// newApp returns an App pointing at a per-test config path and capturing
// stdout/stderr. Lets tests run in parallel without sharing on-disk state.
func newApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	app := &App{Stdout: out, Stderr: errb, ConfigPath: path}
	return app, out, errb, path
}

func TestInitConfig_WritesDefaultGlobalConfig(t *testing.T) {
	// `clawtool init` is the project-setup wizard. The legacy
	// "write a default user-global config.toml" behavior is now
	// reachable via App.Init() as a programmatic helper — exposed
	// separately so tests can exercise it without spawning the
	// wizard. See `clawtool init` (cli_init_wizard.go) for the
	// user-facing entrypoint.
	app, out, _, path := newApp(t)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("Init stdout did not echo path: %q", out.String())
	}
	out.Reset()
	if err := app.Init(); err != nil {
		t.Fatalf("Init second call: %v", err)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("second Init should report 'already exists'; got %q", out.String())
	}
}

func TestToolsList_DefaultStateAfterInit(t *testing.T) {
	app, out, _, _ := newApp(t)
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	out.Reset()
	if rc := app.Run([]string{"tools", "list"}); rc != 0 {
		t.Fatalf("tools list exit = %d, want 0", rc)
	}
	got := out.String()
	if !strings.Contains(got, "Bash") {
		t.Errorf("tools list missing Bash row: %s", got)
	}
	if !strings.Contains(got, "enabled") {
		t.Errorf("tools list missing 'enabled' state: %s", got)
	}
	if !strings.Contains(got, "core_tools.Bash") {
		t.Errorf("tools list rule column should be core_tools.Bash: %s", got)
	}
}

func TestToolsEnableDisableRoundTrip(t *testing.T) {
	app, out, _, _ := newApp(t)

	if rc := app.Run([]string{"tools", "disable", "Bash"}); rc != 0 {
		t.Fatalf("disable exit %d, stderr=...", rc)
	}
	if !strings.Contains(out.String(), "Bash disabled") {
		t.Errorf("disable did not confirm: %q", out.String())
	}

	out.Reset()
	if rc := app.Run([]string{"tools", "status", "Bash"}); rc != 0 {
		t.Fatalf("status exit %d", rc)
	}
	if !strings.Contains(out.String(), "Bash disabled") {
		t.Errorf("status after disable should report disabled, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "tools.Bash") {
		t.Errorf("status should cite the tools-level rule, got: %q", out.String())
	}

	out.Reset()
	if rc := app.Run([]string{"tools", "enable", "Bash"}); rc != 0 {
		t.Fatalf("enable exit %d", rc)
	}
	if !strings.Contains(out.String(), "Bash enabled") {
		t.Errorf("enable did not confirm: %q", out.String())
	}

	out.Reset()
	if rc := app.Run([]string{"tools", "status", "Bash"}); rc != 0 {
		t.Fatalf("status exit %d", rc)
	}
	if !strings.Contains(out.String(), "Bash enabled") {
		t.Errorf("status after enable should report enabled, got: %q", out.String())
	}
}

func TestToolsStatus_SourcedSelectorDefaultsEnabled(t *testing.T) {
	app, out, _, _ := newApp(t)
	if rc := app.Run([]string{"tools", "status", "github-personal.create_issue"}); rc != 0 {
		t.Fatalf("status exit %d", rc)
	}
	got := out.String()
	if !strings.Contains(got, "enabled") {
		t.Errorf("default for sourced selector should be enabled, got: %q", got)
	}
	if !strings.Contains(got, "default") {
		t.Errorf("rule should be default for unconfigured selector, got: %q", got)
	}
}

func TestSelectorValidation_RejectsBadShapes(t *testing.T) {
	cases := []struct {
		argv      []string
		mustFail  bool
		errSubstr string
	}{
		{[]string{"tools", "enable", ""}, true, "selector"},
		{[]string{"tools", "enable", "bash"}, true, "shape"},                          // lowercase, no dot
		{[]string{"tools", "enable", "Github_Personal.create_issue"}, true, "kebab"},  // uppercase letters in instance
		{[]string{"tools", "enable", "github-personal.CreateIssue"}, true, "snake"},   // PascalCase tool
		{[]string{"tools", "enable", "tag:destructive"}, true, "v0.3"},                // not yet wired
		{[]string{"tools", "enable", "group:review-set"}, true, "v0.3"},
		// valid:
		{[]string{"tools", "enable", "Bash"}, false, ""},
		{[]string{"tools", "enable", "github-personal.create_issue"}, false, ""},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.argv, " "), func(t *testing.T) {
			app, _, errb, _ := newApp(t)
			rc := app.Run(c.argv)
			if c.mustFail {
				if rc == 0 {
					t.Fatalf("expected non-zero exit for %v", c.argv)
				}
				if c.errSubstr != "" && !strings.Contains(errb.String(), c.errSubstr) {
					t.Errorf("stderr %q does not contain %q", errb.String(), c.errSubstr)
				}
			} else {
				if rc != 0 {
					t.Fatalf("expected success for %v, exit=%d stderr=%q", c.argv, rc, errb.String())
				}
			}
		})
	}
}

func TestUnknownCommand_ReturnsUsageExit(t *testing.T) {
	app, _, errb, _ := newApp(t)
	rc := app.Run([]string{"frob"})
	if rc != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Errorf("stderr should explain unknown command, got %q", errb.String())
	}
}

func TestNoArgs_ReturnsUsage(t *testing.T) {
	app, _, errb, _ := newApp(t)
	rc := app.Run(nil)
	if rc != 2 {
		t.Errorf("no args exit = %d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "Usage") {
		t.Errorf("stderr should print usage, got %q", errb.String())
	}
}
