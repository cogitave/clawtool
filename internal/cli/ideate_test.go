package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestIdeate_HelpExits0 confirms `clawtool ideate --help` returns
// the canonical usage text without touching disk.
func TestIdeate_HelpExits0(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runIdeate([]string{"--help"})
	if rc != 0 {
		t.Fatalf("rc=%d want 0", rc)
	}
	out := app.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{"clawtool ideate", "--apply", "--source", "adr_questions", "todos", "ci_failures", "manifest_drift", "bench_regression"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q in:\n%s", want, out)
		}
	}
}

// TestIdeate_UnknownFlag returns rc=2 (usage error).
func TestIdeate_UnknownFlag(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runIdeate([]string{"--flombus"})
	if rc != 2 {
		t.Fatalf("rc=%d want 2 (usage)", rc)
	}
}

// TestIdeate_RunOnEmptyRepo returns rc=0 with the (no ideas) message
// when the cwd has nothing for any source to chew on.
func TestIdeate_RunOnEmptyRepo(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runIdeate([]string{"--repo", t.TempDir()})
	if rc != 0 {
		t.Fatalf("rc=%d want 0", rc)
	}
	out := app.Stdout.(*bytes.Buffer).String()
	// Per-source counts always render — that's the rollup the
	// operator uses to spot a quietly broken source.
	if !strings.Contains(out, "per-source counts:") {
		t.Fatalf("missing per-source rollup in:\n%s", out)
	}
}

// TestIdeate_FilterToOneSource accepts --source <name> and the
// orchestrator's "no sources match" path doesn't fire when the name
// is valid.
func TestIdeate_FilterToOneSource(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runIdeate([]string{"--repo", t.TempDir(), "--source", "todos"})
	if rc != 0 {
		t.Fatalf("rc=%d want 0", rc)
	}
}

// TestIdeate_FilterToUnknownSource returns rc=1 — the orchestrator
// treats a no-match filter as a hard error so a typo doesn't
// silently produce zero ideas.
func TestIdeate_FilterToUnknownSource(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	rc := app.runIdeate([]string{"--repo", t.TempDir(), "--source", "nope"})
	if rc != 1 {
		t.Fatalf("rc=%d want 1", rc)
	}
}
