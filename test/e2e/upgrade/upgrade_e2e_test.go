// Package upgrade_e2e drives the binary-swap + `clawtool daemon
// restart` flow inside a Docker container. The harness builds two
// clawtool binaries (v0.0.0-old, v0.0.0-new), installs the old one,
// starts the daemon, swaps the binary on disk, restarts the daemon,
// and asserts /v1/health reports the new version. This catches the
// class of regression where the auto-recovery code path compiles
// + passes unit tests but breaks the actual production upgrade
// because of a path / signal / state-file misstep that only
// surfaces on a real filesystem.
//
// Skipped unless CLAWTOOL_E2E_DOCKER=1 — Docker isn't available in
// every CI lane, and the build takes ~30s. The release pipeline
// will opt in via that env var once we wire it in.
package upgrade_e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestUpgrade_BinarySwapAndDaemonRestart_InContainer is the
// load-bearing assertion: after the binary is swapped on disk,
// `clawtool daemon restart` must bring the daemon up on the new
// version. If the test fails, the upgrade flow is broken and
// shipping a release means every existing user gets the binary
// swap but stays on the old daemon code in memory.
func TestUpgrade_BinarySwapAndDaemonRestart_InContainer(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)

	const tag = "clawtool-e2e-upgrade:test"
	build := exec.Command("docker", "build",
		"-f", filepath.Join("test", "e2e", "upgrade", "Dockerfile"),
		"-t", tag,
		".",
	)
	build.Dir = root
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("docker build: %v", err)
	}

	run := exec.Command("docker", "run", "--rm", tag)
	var out bytes.Buffer
	run.Stdout = &out
	run.Stderr = &out
	runErr := run.Run()

	got := out.String()
	if runErr != nil {
		t.Logf("container output:\n%s", got)
		t.Fatalf("docker run: %v", runErr)
	}

	sections := splitSections(got)

	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("upgrade harness exit = %q, want 0\nfull output:\n%s", exit, got)
	}

	stdout := sections["STDOUT"]
	if !strings.Contains(stdout, "v0.0.0-old") {
		t.Errorf("expected stdout to mention old version v0.0.0-old; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "v0.0.0-new") {
		t.Errorf("expected stdout to mention new version v0.0.0-new (post-restart health); got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PASS — upgrade flow validated end-to-end") {
		t.Errorf("expected final PASS marker; got:\n%s", stdout)
	}
}

// splitSections parses run.sh's marker-delimited output. Same
// shape as the onboard harness — keeps both e2e suites consistent
// so a future refactor of one can lift the helper into a shared
// package without a name collision.
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

// TestSplitSections_ParsesMarkers covers the parser independent of
// Docker so the harness's assertion logic stays trustworthy on CI
// lanes that skip the container build.
func TestSplitSections_ParsesMarkers(t *testing.T) {
	in := strings.Join([]string{
		"build noise",
		"==STDOUT==",
		"old --version: v0.0.0-old",
		"new health: {\"version\":\"v0.0.0-new\"}",
		"PASS — upgrade flow validated end-to-end",
		"==EXIT==",
		"0",
	}, "\n")
	got := splitSections(in)
	for name, want := range map[string]string{
		"EXIT": "0\n",
	} {
		if got[name] != want {
			t.Errorf("section %q = %q, want %q", name, got[name], want)
		}
	}
	if !strings.Contains(got["STDOUT"], "v0.0.0-old") {
		t.Errorf("STDOUT missed old version: %q", got["STDOUT"])
	}
	if !strings.Contains(got["STDOUT"], "v0.0.0-new") {
		t.Errorf("STDOUT missed new version: %q", got["STDOUT"])
	}
	if !strings.Contains(got["STDOUT"], "PASS") {
		t.Errorf("STDOUT missed PASS marker: %q", got["STDOUT"])
	}
}
