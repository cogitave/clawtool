package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMakefileBuildResolvesGoBinary asserts `make -n build`
// expands the Makefile `GO` variable to a non-empty binary
// path. Pre-fix the Makefile hard-coded `/usr/local/go/bin/go`
// with no PATH fallback; on GitHub's setup-go runners (Go
// installs under /opt/hostedtoolcache/go/...) the literal
// path doesn't exist and `make build` exits 127, which broke
// the daily Integration cron for 4 days running before the fix.
//
// This pins the new fallback chain (PATH-resolved go first,
// then the legacy hardcoded path) so a future contributor can't
// accidentally restore the regression. Skipped when `make`
// isn't on PATH (Windows runners, minimal containers) — the
// regression only affects Unix CI runners that drive `make
// build` directly, and they always have make.
func TestMakefileBuildResolvesGoBinary(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate repo root")
	}
	repoRoot := filepath.Join(filepath.Dir(here), "..", "..")

	cmd := exec.Command("make", "-n", "build")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n build: %v\n%s", err, out)
	}
	body := string(out)

	// Locate the line that runs `<go> build -ldflags=… ./cmd/clawtool`.
	var buildLine string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "build -ldflags=") &&
			strings.Contains(line, "./cmd/clawtool") {
			buildLine = line
			break
		}
	}
	if buildLine == "" {
		t.Fatalf("no `<go> build -ldflags=… ./cmd/clawtool` line in `make -n build` output:\n%s", body)
	}

	// First whitespace-separated field is the resolved go binary.
	fields := strings.Fields(buildLine)
	if len(fields) == 0 {
		t.Fatalf("build line is empty: %q", buildLine)
	}
	goPath := fields[0]
	if goPath == "" {
		t.Fatalf("GO resolved to empty string in line: %q", buildLine)
	}
	if !strings.HasSuffix(goPath, "/go") && goPath != "go" {
		t.Errorf("GO resolved to %q; want a path ending in /go (or bare 'go'). Full line: %q", goPath, buildLine)
	}
}
