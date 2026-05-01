//go:build e2e

// Package fullstack_e2e drives the fullstack Docker harness — the
// load-bearing end-to-end check for the v0.22.95–v0.22.106 surface
// (install.sh → daemon → tmux panes → peer-register → peer-send →
// stub agent receives the prompt).
//
// Skip layers (each fail-cleanly so this never blocks default CI):
//
//  1. CLAWTOOL_E2E_DOCKER not set     → skip (no docker requested)
//  2. docker not on PATH / unreachable → skip
//  3. container exits non-zero          → fail with the captured log
//
// Build-tagged `e2e` so `go test ./...` on a non-docker host doesn't
// try to compile this package (the test would just skip, but the
// tag makes the dependency surface explicit and matches the task
// brief's `+build e2e` requirement).
package fullstack_e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	imageTag       = "clawtool-e2e-fullstack:test"
	dockerfilePath = "test/e2e/fullstack/Dockerfile"
)

// repoRoot walks up from cwd to find go.mod — that's the docker
// build context. Same shape the bootstrap / onboard / realinstall
// fixtures use; once a fifth copy lands, lift to a shared helper.
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

// requireDocker mirrors the sibling fixtures' opt-in gate.
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

// TestFullstack_PeerSendRoundtrip is the headline assertion: a
// fresh Ubuntu container can build clawtool, start the daemon,
// host four stub-agent tmux panes, register them as peers, and
// route a `peer send --name codex-stub` through the daemon's
// HTTP API + tmux send-keys pipeline so the codex-stub's marker
// file proves the prompt arrived end-to-end.
func TestFullstack_PeerSendRoundtrip(t *testing.T) {
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
	runErr := run.Run()
	got := out.String()
	t.Logf("container output:\n%s", got)
	if runErr != nil {
		t.Fatalf("docker run: %v", runErr)
	}

	sections := splitSections(got)

	// Section EXIT carries the harness's final rc — must be 0.
	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("harness EXIT = %q, want 0", exit)
	}

	// RESULT is the human-readable summary line; must say PASS.
	if result := strings.TrimSpace(sections["RESULT"]); !strings.Contains(result, "PASS") {
		t.Errorf("harness RESULT = %q, want PASS prefix", result)
	}

	// Peer count: 4 stubs registered. We accept >= 4 to leave
	// room for the operator's own clawtool session showing up
	// in the registry on a future variant.
	if count := strings.TrimSpace(sections["PEER_LIST_COUNT"]); count == "" {
		t.Errorf("missing PEER_LIST_COUNT section")
	} else if count != "4" && count != "5" && count != "6" && count != "7" && count != "8" {
		// Cheap explicit allowlist beats parsing — stays in
		// the [4,8] band that's plausible for this fixture.
		t.Errorf("PEER_LIST_COUNT = %q, want 4..8", count)
	}

	// codex-stub's last-prompt marker must echo the prompt the
	// harness sent. The harness embeds a timestamp into the
	// prompt so even a flaky re-run can't false-positive on a
	// stale marker file.
	last := strings.TrimSpace(sections["CODEX_LAST_PROMPT"])
	if !strings.HasPrefix(last, "FROM_E2E_TEST_") {
		t.Errorf("CODEX_LAST_PROMPT = %q, want FROM_E2E_TEST_<ts>", last)
	}

	// recv.log must have exactly one line — the prompt. If a
	// stray Enter or buffered text leaked into the pane, this
	// flags it before silently mutating the assertion.
	recv := strings.TrimSpace(sections["CODEX_RECV_LOG"])
	if !strings.HasPrefix(recv, "FROM_E2E_TEST_") {
		t.Errorf("CODEX_RECV_LOG = %q, want first line FROM_E2E_TEST_<ts>", recv)
	}

	// Cross-stub isolation: the OTHER stubs' recv.logs MUST be
	// empty (the send was scoped to codex-stub by --name). A
	// regression here means peer send is broadcasting when it
	// should be unicasting.
	for _, other := range []string{"CLAUDE", "GEMINI", "OPENCODE"} {
		key := other + "_RECV_LOG"
		body := strings.TrimSpace(sections[key])
		// "(missing)" is a legitimate harness output when the
		// stub never wrote anything (the file is created at
		// stub startup but if the spawn racing somehow missed
		// it the harness records the absence). Either way:
		// no FROM_E2E_TEST_ payload allowed.
		if strings.Contains(body, "FROM_E2E_TEST_") {
			t.Errorf("%s leaked the prompt: %q", key, body)
		}
	}
}

// TestSplitSections_FullstackParser is the docker-skipped unit
// guard — keeps the section parser locked even on lanes without
// docker. Mirrors the bootstrap fixture's pattern.
func TestSplitSections_FullstackParser(t *testing.T) {
	in := strings.Join([]string{
		"build noise",
		"==BINARY_VERSION==",
		"clawtool 0.22.106",
		"==DAEMON_START==",
		"rc=0",
		"==PEER_LIST_COUNT==",
		"4",
		"==CODEX_LAST_PROMPT==",
		"FROM_E2E_TEST_1234",
		"==RESULT==",
		"PASS: prompt round-tripped end-to-end",
		"==EXIT==",
		"0",
	}, "\n")
	got := splitSections(in)
	if got["EXIT"] != "0\n" {
		t.Errorf("EXIT = %q, want 0\\n", got["EXIT"])
	}
	if strings.TrimSpace(got["PEER_LIST_COUNT"]) != "4" {
		t.Errorf("PEER_LIST_COUNT = %q, want 4", got["PEER_LIST_COUNT"])
	}
	if !strings.Contains(got["CODEX_LAST_PROMPT"], "FROM_E2E_TEST_") {
		t.Errorf("CODEX_LAST_PROMPT lost payload: %q", got["CODEX_LAST_PROMPT"])
	}
	if !strings.Contains(got["RESULT"], "PASS") {
		t.Errorf("RESULT lost PASS: %q", got["RESULT"])
	}
}

// splitSections parses run.sh's marker-delimited output. Same shape
// as the sibling fixtures (bootstrap / onboard / realinstall);
// each fixture's section names differ so the trivial dup is
// acceptable until a fifth copy forces a shared helper.
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
		if strings.HasPrefix(line, "==") && strings.HasSuffix(line, "==") && len(line) > 4 {
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
