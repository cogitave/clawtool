// Package bootstrap_e2e drives `clawtool bootstrap --agent claude`
// inside a Docker container that has a STUB claude binary on PATH.
// This fixture intentionally lands BEFORE the bootstrap verb does:
// the parallel branch (autodev/bootstrap-claude-init) wires the
// verb itself, and once that branch merges this test flips from
// "skipped — verb not present" to a real assertion automatically.
//
// Skip layers (each fail-cleanly so this test never blocks CI):
//
//  1. CLAWTOOL_E2E_DOCKER not set    → skip (no docker requested)
//  2. docker not on PATH / unreachable → skip
//  3. `clawtool bootstrap --help` exits non-zero inside the
//     container → skip with "bootstrap verb not yet present on
//     this commit" (the load-bearing skip the task brief calls
//     out — proves harness compiles + runs while we wait for the
//     parallel branch).
//
// When the verb DOES exist, the assertions are:
//   - bootstrap exits 0
//   - the stub claude binary recorded at least one invocation
//     (proving the verb actually shelled out to `claude`)
//
// The stub's stdin capture (/tmp/claude.stdin) is surfaced for
// debugging but not strictly asserted — different bootstrap
// implementations might pass the prompt via argv or a tmpfile,
// and we don't want to overconstrain the verb's wire shape from
// a sibling branch.
package bootstrap_e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	imageTag       = "clawtool-e2e-bootstrap:test"
	dockerfilePath = "test/e2e/bootstrap/Dockerfile"
)

// repoRoot walks up from cwd to find go.mod — that's the docker
// build context. Same shape the onboard / realinstall fixtures
// use; once a fourth copy lands, lift to a shared helper.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find repo root (no go.mod above %q)", dir)
		}
		dir = parent
	}
}

// requireDocker mirrors the onboard / realinstall gate. The opt-in
// env var keeps `go test ./...` on a non-docker host clean.
func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("CLAWTOOL_E2E_DOCKER") != "1" {
		t.Skip("set CLAWTOOL_E2E_DOCKER=1 to run docker-backed e2e tests")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker binary not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// TestBootstrap_StubClaude_InContainer is the load-bearing
// assertion: a fresh container can run `clawtool bootstrap --agent
// claude` against a stub claude binary, the verb completes
// cleanly, and the stub records at least one invocation.
//
// SKIPPED on commits that don't have the bootstrap verb yet — the
// parallel branch (autodev/bootstrap-claude-init) lands the verb
// and this test flips into a real check then.
func TestBootstrap_StubClaude_InContainer(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)

	build := exec.Command("docker", "build",
		"-f", dockerfilePath,
		"-t", imageTag,
		".",
	)
	build.Dir = root
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("docker build: %v", err)
	}

	run := exec.Command("docker", "run", "--rm", imageTag)
	var out bytes.Buffer
	run.Stdout = &out
	run.Stderr = &out
	if err := run.Run(); err != nil {
		t.Logf("container output:\n%s", out.String())
		t.Fatalf("docker run: %v", err)
	}

	got := out.String()
	sections := splitSections(got)

	// Verb-present probe is the FIRST thing the run.sh harness
	// emits; if the verb hasn't merged yet, skip cleanly so this
	// fixture can land independently of the parallel branch.
	if present := strings.TrimSpace(sections["VERB_PRESENT"]); present != "yes" {
		t.Skipf("bootstrap verb not yet present on this commit (probe=%q)\nfull output:\n%s", present, got)
	}

	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("bootstrap exit = %q, want 0\nstdout:\n%s\nstderr:\n%s",
			exit, sections["STDOUT"], sections["STDERR"])
	}

	// Stub-invocation evidence: the verb must have shelled out
	// to `claude` at least once. The exact argv shape is
	// intentionally unconstrained here — the parallel branch
	// owns that decision — but the count must be >= 1.
	invs := strings.TrimSpace(sections["STUB_INVOCATIONS"])
	if invs == "" || invs == "(none)" {
		t.Errorf("expected stub claude to be invoked at least once; got %q", invs)
	}
	t.Logf("stub claude invocations:\n%s", invs)
	t.Logf("stub claude stdin capture:\n%s", strings.TrimSpace(sections["STUB_STDIN"]))
}

// TestSplitSections_BootstrapParser is the docker-skipped unit
// guard — keeps the section parser locked even on CI lanes
// without docker, mirroring the realinstall fixture's pattern.
func TestSplitSections_BootstrapParser(t *testing.T) {
	in := strings.Join([]string{
		"build noise",
		"==STDOUT==",
		"bootstrap ran",
		"==STDERR==",
		"",
		"==EXIT==",
		"0",
		"==VERB_PRESENT==",
		"yes",
		"==STUB_INVOCATIONS==",
		"/usr/local/bin/claude --version",
		"==STUB_STDIN==",
		"hello",
	}, "\n")
	got := splitSections(in)
	if got["EXIT"] != "0\n" {
		t.Errorf("EXIT = %q, want 0\\n", got["EXIT"])
	}
	if strings.TrimSpace(got["VERB_PRESENT"]) != "yes" {
		t.Errorf("VERB_PRESENT = %q, want yes", got["VERB_PRESENT"])
	}
	if !strings.Contains(got["STUB_INVOCATIONS"], "claude --version") {
		t.Errorf("STUB_INVOCATIONS lost payload: %q", got["STUB_INVOCATIONS"])
	}
	if !strings.Contains(got["STUB_STDIN"], "hello") {
		t.Errorf("STUB_STDIN lost payload: %q", got["STUB_STDIN"])
	}
}

// splitSections parses run.sh's marker-delimited output. Same
// shape as onboard / realinstall — once a fourth copy exists
// we should lift it to a shared helper, but each fixture's
// section names differ so the trivial dup is acceptable for now.
func splitSections(s string) map[string]string {
	out := map[string]string{}
	var cur string
	var buf bytes.Buffer
	flush := func() {
		if cur != "" {
			out[cur] = buf.String()
		}
		buf.Reset()
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "==") && strings.HasSuffix(line, "==") {
			flush()
			cur = strings.Trim(line, "=")
			continue
		}
		if cur == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	flush()
	return out
}
