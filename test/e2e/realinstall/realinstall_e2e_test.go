// Package realinstall_e2e drives the install.sh + GitHub-release
// download + onboard + daemon-lifecycle flow inside an Alpine
// container. Unlike the upgrade and onboard fixtures (which build
// clawtool from source via go build), this one tests the path a
// real user hits: `curl install.sh | sh`, which in turn fetches
// the actual release tarball from cogitave/clawtool's GitHub
// releases. The harness:
//
//   1. Verifies install.sh placed the binary at the configured
//      location and that it runs (catches musl-vs-glibc linkage
//      regressions on Alpine).
//   2. Starts the daemon, probes /v1/health, lists core tools.
//   3. Renders `clawtool overview` for sanity.
//   4. Runs `clawtool upgrade --check` (real network round-trip
//      to GitHub for the release feed).
//   5. Drives `clawtool onboard --yes` against mock claude /
//      codex / gemini CLIs so the wizard's full state machine
//      fires.
//   6. Stops the daemon and confirms state-file cleanup.
//
// Skipped unless CLAWTOOL_E2E_DOCKER=1. The container is
// deliberately NOT auto-removed so the operator can inspect
// state in Docker Desktop after the test runs; cleanup hint
// surfaced via t.Logf at the end.
package realinstall_e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	imageTag       = "clawtool-e2e-realinstall:test"
	containerName  = "clawtool-e2e-realinstall"
	e2eLabel       = "clawtool.e2e=realinstall"
	dockerfilePath = "test/e2e/realinstall/Dockerfile"
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

// TestRealInstall_AlpineFromGitHubRelease is the load-bearing
// assertion: a fresh Alpine box can run install.sh, end up with
// a working daemon, and complete the onboard wizard end-to-end.
// If this fails, real new-user installs are broken — same blast
// radius as the upgrade test, on the upstream side.
func TestRealInstall_AlpineFromGitHubRelease(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)

	// Clean any container left behind by a prior run. We tolerate
	// failure (no container = nothing to remove).
	_ = exec.Command("docker", "rm", "-f", containerName).Run()

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

	// Note: no `--rm` — container stays in Docker Desktop after
	// the test exits so the operator can `docker exec` into it
	// or inspect filesystem state. Cleanup hint via t.Logf at
	// the end.
	run := exec.Command("docker", "run",
		"--name", containerName,
		"--label", e2eLabel,
		imageTag,
	)
	var out bytes.Buffer
	run.Stdout = &out
	run.Stderr = &out
	runErr := run.Run()

	got := out.String()
	if runErr != nil {
		t.Logf("container output:\n%s", got)
		t.Fatalf("docker run: %v\n(container left behind for inspection: docker logs %s)", runErr, containerName)
	}

	sections := splitSections(got)

	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("realinstall harness exit = %q, want 0\nfull output:\n%s", exit, got)
	}

	stdout := sections["STDOUT"]
	// Each stage's success marker — if any of these are missing
	// the install path broke at that stage. Output them as
	// individual sub-checks so a failing run surfaces exactly
	// which step regressed.
	wantMarkers := []string{
		"install.sh placed binary at",
		"binary runs and reports a version string",
		"daemon answers /v1/health",
		"tools list shows at least 4 core tools",
		"overview rendered",
		"upgrade --check completed",
		"onboard wrote the .onboarded marker",
		"daemon stopped + state file cleaned up",
		"PASS — clean install + daemon + onboard + upgrade-check flow",
	}
	for _, want := range wantMarkers {
		if !strings.Contains(stdout, want) {
			t.Errorf("missing stage marker %q in container stdout:\n%s", want, stdout)
		}
	}

	// Mock CLI invocation count — onboard --yes must have probed
	// at least one of claude/codex/gemini (its primary-CLI
	// detection step).
	if !strings.Contains(stdout, "claude:") && !strings.Contains(stdout, "codex:") && !strings.Contains(stdout, "gemini:") {
		t.Errorf("expected at least one mock CLI invocation report; got:\n%s", stdout)
	}

	t.Logf("✓ container %s left in Exited state; inspect via Docker Desktop", containerName)
	t.Logf("  cleanup: docker rm -f %s", containerName)
}

// splitSections parses run.sh's marker-delimited output into a
// map keyed by section name. Same shape the upgrade fixture
// uses; once we land a third copy, lift to a shared helper.
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

// TestSplitSections_RealInstallParser is the docker-skipped unit
// guard — keeps the splitSections logic locked even on CI lanes
// without docker.
func TestSplitSections_RealInstallParser(t *testing.T) {
	in := strings.Join([]string{
		"build noise",
		"==STDOUT==",
		"→ Stage 1: run install.sh",
		"✓ install.sh placed binary at /usr/local/bin/clawtool",
		"PASS — clean install + daemon + onboard + upgrade-check flow",
		"==EXIT==",
		"0",
	}, "\n")
	got := splitSections(in)
	if got["EXIT"] != "0\n" {
		t.Errorf("EXIT section = %q, want 0\\n", got["EXIT"])
	}
	if !strings.Contains(got["STDOUT"], "Stage 1") {
		t.Errorf("STDOUT lost Stage 1 line: %q", got["STDOUT"])
	}
	if !strings.Contains(got["STDOUT"], "PASS") {
		t.Errorf("STDOUT lost PASS marker: %q", got["STDOUT"])
	}
}
