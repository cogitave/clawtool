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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// imageTag is the docker image both tests build against.
const imageTag = "clawtool-e2e-upgrade:test"

// e2eLabel is stamped on every container this suite spawns so
// the operator can `docker ps -f label=clawtool.e2e=upgrade` to
// see exactly what the test left behind.
const e2eLabel = "clawtool.e2e=upgrade"

// buildImage compiles the e2e image once per test process. Idempotent
// — Docker re-uses the cache when nothing changed; subsequent calls
// inside the same `go test` run finish in <1s.
func buildImage(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	build := exec.Command("docker", "build",
		"-f", filepath.Join("test", "e2e", "upgrade", "Dockerfile"),
		"-t", imageTag,
		".",
	)
	build.Dir = root
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("docker build: %v", err)
	}
	return imageTag
}

// killStaleContainer force-removes a named container from a prior
// test run if one is still around. Without this, two consecutive
// `go test` invocations would collide on the deterministic name.
// We tolerate failure (container may not exist).
func killStaleContainer(name string) {
	_ = exec.Command("docker", "rm", "-f", name).Run()
}

// TestUpgrade_BinarySwapAndDaemonRestart_InContainer is the
// load-bearing assertion: after the binary is swapped on disk,
// `clawtool daemon restart` must bring the daemon up on the new
// version. If the test fails, the upgrade flow is broken and
// shipping a release means every existing user gets the binary
// swap but stays on the old daemon code in memory.
//
// Container is named (`clawtool-e2e-upgrade-oneshot`) and labelled
// (`clawtool.e2e=upgrade`) so it shows up in Docker Desktop's
// container list AFTER the test finishes — the operator can
// inspect the post-test state, then `docker rm
// clawtool-e2e-upgrade-oneshot` when done. We deliberately don't
// pass `--rm`; the previous shape ate the container the moment
// the harness exited, leaving Desktop empty.
func TestUpgrade_BinarySwapAndDaemonRestart_InContainer(t *testing.T) {
	requireDocker(t)
	tag := buildImage(t)

	const name = "clawtool-e2e-upgrade-oneshot"
	killStaleContainer(name)

	run := exec.Command("docker", "run",
		"--name", name,
		"--label", e2eLabel,
		tag,
	)
	var out bytes.Buffer
	run.Stdout = &out
	run.Stderr = &out
	runErr := run.Run()

	got := out.String()
	if runErr != nil {
		t.Logf("container output:\n%s", got)
		t.Fatalf("docker run: %v\n(container left behind for inspection: docker logs %s)", runErr, name)
	}

	sections := splitSections(got)

	if exit := strings.TrimSpace(sections["EXIT"]); exit != "0" {
		t.Errorf("upgrade harness exit = %q, want 0\nfull output:\n%s", exit, got)
	}

	stdout := sections["STDOUT"]
	// version.Resolved() strips a leading `v` from the
	// ldflags-injected version string, so `--version` and
	// `/v1/health` both report `0.0.0-old` / `0.0.0-new` not
	// `v0.0.0-...`. Assertions match the canonical form.
	if !strings.Contains(stdout, "0.0.0-old") {
		t.Errorf("expected stdout to mention old version 0.0.0-old; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "0.0.0-new") {
		t.Errorf("expected stdout to mention new version 0.0.0-new (post-restart health); got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PASS — upgrade flow validated end-to-end") {
		t.Errorf("expected final PASS marker; got:\n%s", stdout)
	}

	// Container intentionally left in `Exited` state so the
	// operator sees it in Docker Desktop. Surface the cleanup
	// command so tests don't accumulate forever.
	t.Logf("✓ container %s left in place; clean up with `docker rm %s`", name, name)
}

// TestUpgrade_LiveContainerSurvivesBinarySwap models the production
// "user keeps the daemon running, runs upgrade against it" path:
// the container stays in Docker Desktop's RUNNING list throughout,
// the host drives the binary swap + restart via `docker exec`,
// and we assert /v1/health flips from old → new without taking
// the container down. This is the assertion that catches "binary
// swap killed the daemon and it never came back" regressions.
//
// At the end, the container is still running on the new version —
// the operator can attach to Docker Desktop, click into the
// container's console, and see for themselves that the daemon
// recovered. Cleanup hint surfaced via t.Logf.
func TestUpgrade_LiveContainerSurvivesBinarySwap(t *testing.T) {
	requireDocker(t)
	tag := buildImage(t)

	const name = "clawtool-e2e-upgrade-live"
	killStaleContainer(name)

	// Detached run with the long-running entrypoint so the
	// container stays alive while the host drives upgrade.
	startArgs := []string{
		"run", "-d",
		"--name", name,
		"--label", e2eLabel,
		"--entrypoint", "/usr/local/bin/long_running.sh",
		tag,
	}
	if err := exec.Command("docker", startArgs...).Run(); err != nil {
		t.Fatalf("docker run -d: %v", err)
	}
	t.Logf("container %s started; if the test fails, inspect: docker logs %s", name, name)

	// Wait for DAEMON_READY marker via `docker logs`. Up to ~10s
	// for the daemon to come up and write daemon.json.
	if err := waitForLogLine(t, name, "DAEMON_READY", 10*time.Second); err != nil {
		_ = exec.Command("docker", "logs", name).Run() // best-effort surface
		t.Fatalf("waiting for DAEMON_READY: %v", err)
	}

	// Sanity probe: container's clawtool reports v0.0.0-old.
	if v := dockerExec(t, name, "/usr/local/bin/clawtool", "--version"); !strings.Contains(v, "0.0.0-old") {
		t.Fatalf("pre-swap --version = %q, want substring 0.0.0-old", v)
	}

	// Atomic binary swap inside the running container — same shape
	// `clawtool upgrade` produces post-selfupdate.UpdateTo.
	dockerExec(t, name, "cp", "/opt/clawtool-new", "/usr/local/bin/clawtool.new")
	dockerExec(t, name, "mv", "/usr/local/bin/clawtool.new", "/usr/local/bin/clawtool")
	if v := dockerExec(t, name, "/usr/local/bin/clawtool", "--version"); !strings.Contains(v, "0.0.0-new") {
		t.Fatalf("post-swap --version = %q, want substring 0.0.0-new", v)
	}

	// Drive `daemon restart` from the host. This is the bit that
	// `clawtool upgrade`'s restartDaemonIfRunning helper invokes
	// on the operator's machine — calling it here is the
	// closest container-test approximation of running upgrade
	// against a live daemon.
	out := dockerExec(t, name, "/usr/local/bin/clawtool", "daemon", "restart")
	if !strings.Contains(out, "✓ daemon ready") && !strings.Contains(out, "daemon ready") {
		t.Errorf("daemon restart output missing ready marker:\n%s", out)
	}

	// Probe /v1/health from inside the container. The new daemon
	// picked a fresh port; read it from daemon.json the same way
	// the live binary writes it.
	healthCmd := `set -e
PORT=$(grep -oP '"port":\s*\K[0-9]+' /tmp/cfg/clawtool/daemon.json)
TOKEN=$(tr -d '\n' < /tmp/cfg/clawtool/listener-token)
curl -fsS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/v1/health"`
	health := dockerExecBash(t, name, healthCmd)
	if !strings.Contains(health, "0.0.0-new") {
		t.Errorf("post-restart /v1/health = %q, want version 0.0.0-new", health)
	}
	if !strings.Contains(health, `"status":"ok"`) {
		t.Errorf("post-restart /v1/health missing status:ok, got %q", health)
	}

	// Container is still running and on the new version. We do
	// NOT stop it — the whole point is operator visibility in
	// Docker Desktop.
	t.Logf("✓ container %s still running on v0.0.0-new; inspect via Docker Desktop", name)
	t.Logf("  cleanup: docker rm -f %s", name)
}

// dockerExec runs a command inside the named container and
// returns combined stdout+stderr. Fails the test on non-zero
// exit; surfaces the output so a failing assertion can show
// what the container actually said.
func dockerExec(t *testing.T, container string, argv ...string) string {
	t.Helper()
	args := append([]string{"exec", container}, argv...)
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s %v: %v\noutput: %s", container, argv, err, out)
	}
	return string(out)
}

// dockerExecBash runs a multi-line bash script inside the named
// container. Convenience wrapper around dockerExec for the
// `daemon.json → port → curl` flow that doesn't fit a single argv.
func dockerExecBash(t *testing.T, container, script string) string {
	t.Helper()
	cmd := exec.Command("docker", "exec", container, "bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %s bash -c <script>: %v\nscript:\n%s\noutput: %s",
			container, err, script, out)
	}
	return string(out)
}

// waitForLogLine polls `docker logs` until the specified
// substring appears or the timeout elapses. Used to wait for
// the long-running entrypoint's DAEMON_READY marker.
func waitForLogLine(t *testing.T, container, marker string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "logs", container).CombinedOutput()
		if err == nil && strings.Contains(string(out), marker) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("marker %q not seen in %s logs within %s", marker, container, timeout)
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
		"old --version: clawtool 0.0.0-old",
		"new health: {\"version\":\"0.0.0-new\"}",
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
	if !strings.Contains(got["STDOUT"], "0.0.0-old") {
		t.Errorf("STDOUT missed old version: %q", got["STDOUT"])
	}
	if !strings.Contains(got["STDOUT"], "0.0.0-new") {
		t.Errorf("STDOUT missed new version: %q", got["STDOUT"])
	}
	if !strings.Contains(got["STDOUT"], "PASS") {
		t.Errorf("STDOUT missed PASS marker: %q", got["STDOUT"])
	}
}
