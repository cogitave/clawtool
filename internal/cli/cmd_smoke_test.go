//go:build smoke

// Smoke-test: every top-level clawtool verb's `--help` plus a curated
// set of read-only listings. Gated by `//go:build smoke` so default
// `go test ./...` stays quick.
//
//	go build -o bin/clawtool ./cmd/clawtool
//	go test -tags=smoke -count=1 ./internal/cli/ -run TestCLI_AllVerbs -v

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// usageBannerRE: any of "Usage:", "Usage of " (stdlib flag.PrintDefaults
// — claude-bootstrap), or a leading "clawtool" line.
var usageBannerRE = regexp.MustCompile(`(?m)^\s*(Usage:|Usage of\s|clawtool\s)`)

// verbLineRE: leading-indent + "clawtool <verb>" lines in topUsage.
var verbLineRE = regexp.MustCompile(`(?m)^\s+clawtool\s+([a-z][a-z0-9-]*)\b`)

func findBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..")) // internal/cli -> repo root
	bin := filepath.Join(root, "bin", "clawtool")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	t.Logf("smoke: binary missing, building")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/clawtool")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("smoke: go build failed: %v\n%s", err, out)
	}
	return bin
}

// runWithTimeout exec()s the binary with a 10s ceiling and a sterile
// CI-shaped env so menu-style verbs don't try to attach a TTY.
func runWithTimeout(bin string, argv []string) (stdout, stderr string, exitCode int) {
	cmd := exec.Command(bin, argv...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.Env = append(os.Environ(), "CLAWTOOL_NO_TUI=1", "CI=1", "TERM=dumb")
	if err := cmd.Start(); err != nil {
		return "", err.Error(), -1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return so.String(), se.String(), ee.ExitCode()
			}
			return so.String(), se.String() + err.Error(), -1
		}
		return so.String(), se.String(), 0
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return so.String(), se.String() + "\n[smoke: timed out]", -2
	}
}

// discoverVerbs parses `<bin> --help` and folds in any verb from the
// fallback inventory the dispatcher accepts (probed via `<verb> --help`,
// rejected if output contains "unknown command"). Both signals come
// from the binary at test time. dangerousProbe verbs are taken on
// faith — probing init or serve mutates the host or blocks.
func discoverVerbs(t *testing.T, bin string) []string {
	t.Helper()
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("smoke: --help failed: %v\n%s", err, out)
	}
	seen := map[string]bool{}
	var verbs []string
	for _, m := range verbLineRE.FindAllStringSubmatch(string(out), -1) {
		if v := m[1]; v != "clawtool" && !seen[v] {
			seen[v] = true
			verbs = append(verbs, v)
		}
	}
	if len(verbs) == 0 {
		t.Fatalf("smoke: parsed zero verbs from --help:\n%s", out)
	}
	dangerous := map[string]bool{"serve": true, "init": true}
	for _, want := range inventoryFallbackVerbs {
		if seen[want] {
			continue
		}
		if dangerous[want] {
			seen[want] = true
			verbs = append(verbs, want)
			continue
		}
		so, se, _ := runWithTimeout(bin, []string{want, "--help"})
		if strings.Contains(so+se, "unknown command") {
			t.Logf("smoke: fallback verb %q rejected — dropping", want)
			continue
		}
		seen[want] = true
		verbs = append(verbs, want)
	}
	return verbs
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func TestCLI_AllVerbs(t *testing.T) {
	bin := findBinary(t)
	verbs := discoverVerbs(t, bin)
	t.Logf("smoke: discovered %d verbs: %s", len(verbs), strings.Join(verbs, " "))

	type failure struct {
		argv []string
		code int
		err  string
		hint string
	}
	var failures []failure
	add := func(argv []string, code int, stderr, hint string) {
		failures = append(failures, failure{argv, code, firstLine(stderr), hint})
	}

	// init mutates cwd under non-TTY; serve blocks on stdin.
	skipHelp := map[string]string{
		"serve": "boots MCP server, blocks on stdin",
		"init":  "wizard mutates cwd repo before --help error path",
	}

	// Phase 1: <verb> --help. Accept rc 0 (help printer) or rc 2
	// (group dispatcher rejects --help as unknown subcommand and
	// falls through to the usage printer) so long as a banner shows.
	for _, v := range verbs {
		if reason, skip := skipHelp[v]; skip {
			t.Logf("smoke: skipping `%s --help` (%s)", v, reason)
			continue
		}
		argv := []string{v, "--help"}
		so, se, code := runWithTimeout(bin, argv)
		if !usageBannerRE.MatchString(so + "\n" + se) {
			add(argv, code, se, "no usage banner")
			continue
		}
		if code != 0 && code != 2 {
			add(argv, code, se, "unexpected exit code (want 0 or 2)")
		}
	}

	// Phase 2: read-only listings. Exit 0 + non-empty stdout.
	for _, argv := range readOnlySubcommands {
		so, se, code := runWithTimeout(bin, argv)
		switch {
		case code != 0:
			add(argv, code, se, "exited non-zero")
		case strings.TrimSpace(so) == "":
			add(argv, code, se, "empty stdout")
		}
	}

	if len(failures) == 0 {
		t.Logf("smoke: all %d verbs + %d read-only subcommands passed", len(verbs), len(readOnlySubcommands))
		return
	}
	for _, f := range failures {
		t.Errorf("FAIL %s: exit=%d stderr=%q hint=%s",
			strings.Join(f.argv, " "), f.code, f.err, f.hint)
	}
}
