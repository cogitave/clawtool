// Package onboard_e2e drives `clawtool onboard --yes` inside a
// Docker container that has mock claude / codex / gemini binaries on
// PATH. The test asserts the wizard runs without prompting, the
// onboarded marker lands at ~/.config/clawtool/.onboarded, and the
// star CTA + per-step telemetry funnel show up in stdout.
//
// Skipped unless CLAWTOOL_E2E_DOCKER=1 — Docker isn't available in
// every CI lane, and building the container ad-hoc takes ~30s. The
// release pipeline will opt in via that env var once we wire it.
package onboard_e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test file to find the directory holding
// `go.mod` — that's the docker build context the Dockerfile expects.
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

// requireDocker fails the test cleanly when Docker isn't reachable.
// Same pattern Go's stdlib uses for tests that need an external
// binary; we don't want a flake-storm in environments without it.
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

// TestOnboard_YesMode_InContainer is the load-bearing assertion:
// build the e2e image, run it, parse the marker-delimited sections
// out of stdout, confirm the onboard wizard ran cleanly under
// --yes, the .onboarded marker landed, and the star CTA + per-step
// progress lines show up. Docker stderr leaks into our stdout via
// the `bash` entrypoint, but each captured section is delimited so
// the test can split cleanly.
func TestOnboard_YesMode_InContainer(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)

	const tag = "clawtool-e2e-onboard:test"
	build := exec.Command("docker", "build",
		"-f", filepath.Join("test", "e2e", "onboard", "Dockerfile"),
		"-t", tag,
		".",
	)
	build.Dir = root
	build.Stdout = os.Stderr // surface build progress on test failure
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("docker build: %v", err)
	}

	run := exec.Command("docker", "run", "--rm", tag)
	var out bytes.Buffer
	run.Stdout = &out
	run.Stderr = &out
	if err := run.Run(); err != nil {
		t.Logf("container output:\n%s", out.String())
		t.Fatalf("docker run: %v", err)
	}

	got := out.String()
	sections := splitSections(got)

	// onboard exit code must be 0 (the wizard finished cleanly).
	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("onboard exit = %q, want 0\nfull output:\n%s", exit, got)
	}

	// Marker must exist — proves writeOnboardedMarker ran.
	if marker := strings.TrimSpace(sections["MARKER"]); marker == "ABSENT" || marker == "" {
		t.Errorf("expected .onboarded marker present, got %q", marker)
	}

	// Stdout must include the star CTA — proves the closing block
	// ran and the wizard finished its full pass.
	stdout := sections["STDOUT"]
	if !strings.Contains(stdout, "github.com/cogitave/clawtool") {
		t.Errorf("expected star CTA referencing github.com/cogitave/clawtool in stdout; got:\n%s", stdout)
	}

	// Per-step progress markers (from the side-effect dispatch
	// loop). At minimum the wizard should mention the daemon.
	for _, want := range []string{"daemon", "BIAM identity", "secrets store"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected stdout to mention %q; got:\n%s", want, stdout)
		}
	}
}

// TestSplitSections_ParsesMarkers covers the parser independent of
// Docker so the harness's assertion logic stays trustworthy even on
// CI lanes that skip the container build. The parser is the part
// most likely to break silently — adding an extra section or
// renaming one in run.sh would otherwise just produce empty
// asserts.
func TestSplitSections_ParsesMarkers(t *testing.T) {
	in := strings.Join([]string{
		"build noise we should drop",
		"==STDOUT==",
		"line one",
		"line two",
		"==STDERR==",
		"oops",
		"==EXIT==",
		"0",
		"==MARKER==",
		"2026-04-28T14:55:00Z",
		"==MOCK_LOGS==",
		"--- claude.invocations ---",
		"claude --version",
	}, "\n")
	got := splitSections(in)

	for name, want := range map[string]string{
		"STDOUT": "line one\nline two\n",
		"STDERR": "oops\n",
		"EXIT":   "0\n",
		"MARKER": "2026-04-28T14:55:00Z\n",
	} {
		if got[name] != want {
			t.Errorf("section %q = %q, want %q", name, got[name], want)
		}
	}
	if !strings.Contains(got["MOCK_LOGS"], "claude --version") {
		t.Errorf("MOCK_LOGS section missed payload: %q", got["MOCK_LOGS"])
	}
}

// splitSections parses run.sh's marker-delimited output into a
// map keyed by section name (`STDOUT`, `STDERR`, `EXIT`,
// `MARKER`, `MOCK_LOGS`). Anything before the first marker is
// dropped (defensive: the build step's progress won't pollute
// the assertions).
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
